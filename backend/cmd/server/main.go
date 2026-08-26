package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/village/backend/internal/backfill"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/handler"
	"github.com/peasant-labs/village/backend/internal/router"
	"github.com/peasant-labs/village/backend/internal/storage"
)

var constructObjectStore = storage.NewS3ObjectStore
var startHTTPServing = serve
var constructTitlePipeline = redact.NewTitlePipeline
var executeTitleBackfill = func(ctx context.Context, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, titles *redact.TitlePipeline, mode backfill.TitleBackfillMode) (backfill.TitleBackfillResult, error) {
	job, err := backfill.NewTitleBackfill(pool, blobs, titles, slog.Default(), backfill.DefaultTitleBackfillBatchSize)
	if err != nil {
		return backfill.TitleBackfillResult{}, err
	}
	return job.Run(ctx, mode)
}

var executeOriginBackfill = func(ctx context.Context, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, mode backfill.OriginBackfillMode) (backfill.OriginBackfillResult, error) {
	job, err := backfill.NewOriginBackfill(pool, blobs, slog.Default(), backfill.DefaultOriginBackfillBatchSize)
	if err != nil {
		return backfill.OriginBackfillResult{}, err
	}
	return job.Run(ctx, mode)
}

func main() {
	godotenv.Load("../.env")
	if err := run(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	selection, err := parseRuntimeSelection(args)
	if err != nil {
		return err
	}
	authority, err := config.LoadAuthority(selection.AuthorityRequirements())
	if err != nil {
		return err
	}
	poolCfg, err := database.PoolConfig(authority.Config.DatabaseURL)
	if err != nil {
		return fmt.Errorf("runtime startup failed because database pool configuration was rejected in run after authority validation; no job or listener started; correct DATABASE_URL and retry: %w", err)
	}
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(connectCtx, poolCfg)
	if err != nil {
		return fmt.Errorf("runtime startup failed because PostgreSQL could not be opened in run after authority validation; no job or listener started; restore database connectivity and retry: %w", err)
	}
	defer pool.Close()
	if err = pool.Ping(connectCtx); err != nil {
		return fmt.Errorf("runtime startup failed because PostgreSQL did not answer in run before migration; no job or listener started; restore database connectivity and retry: %w", err)
	}
	if err = database.RunMigrations(pool); err != nil {
		return fmt.Errorf("runtime startup failed because database migration failed in run before mode dispatch; no job or listener started; repair the migration precondition and retry: %w", err)
	}
	if selection.Mode() == runtimeModeMigrateOnly {
		return nil
	}
	if selection.Mode() == runtimeModeShareStateCheck {
		// Handled here, beside migrate-only, so it never constructs blob storage
		// or a key authority it has no use for.
		return reportShareStateConsistency(ctx, pool)
	}
	titles, err := constructTitlePipeline()
	if err != nil {
		return fmt.Errorf("runtime startup failed because the title safety pipeline could not be constructed in run before mode dispatch; title writes cannot be safely served and no job or listener started; restore the public redact dependency and restart: %w", err)
	}
	return withTranscriptStorage(authority.Config, authority.Keyring, func(blobs storage.TranscriptBlobStore) error {
		return dispatchRuntime(ctx, selection, authority.Config, authority.Keyring, pool, blobs, titles)
	})
}

func dispatchRuntime(ctx context.Context, selection runtimeSelection, cfg *config.Config, keyring *config.TranscriptKeyring, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, titles *redact.TitlePipeline) error {
	switch selection.Mode() {
	case runtimeModeContentIdentityBackfill:
		result, err := backfill.ContentIdentity(ctx, pool, blobs)
		if err != nil {
			return err
		}
		if result.Failed > 0 {
			return fmt.Errorf("content identity backfill completed with %d failed rows in run; successful rows remain installed but the operation is incomplete; correct logged row failures and rerun", result.Failed)
		}
		return nil
	case runtimeModeTitleBackfill:
		result, err := executeTitleBackfill(ctx, pool, blobs, titles, selection.TitleBackfillMode())
		slog.Info("title_backfill_complete",
			"mode", selection.TitleBackfillMode().String(),
			"scanned", result.Scanned,
			"unchanged", result.Unchanged,
			"would_update", result.WouldUpdate,
			"updated", result.Updated,
			"derived", result.Derived,
			"sanitized", result.Sanitized,
			"failed", result.Failed)
		return err
	case runtimeModeOriginBackfill:
		result, err := executeOriginBackfill(ctx, pool, blobs, selection.OriginBackfillMode())
		slog.Info("origin_backfill_complete",
			"mode", selection.OriginBackfillMode().String(),
			"scanned", result.Scanned,
			"unchanged", result.Unchanged,
			"would_update", result.WouldUpdate,
			"updated", result.Updated,
			"failed", result.Failed)
		return err
	case runtimeModeRewrap:
		result, err := backfill.Rewrap(ctx, pool, blobs, keyring.ActiveVersion(), selection.RewrapLimit())
		if err != nil {
			return err
		}
		if result.Failed+result.Uncertain > 0 {
			return fmt.Errorf("key rewrap completed with failed=%d uncertain=%d in run; successful rotations remain installed but completion is not certain; repair failures and reconcile uncertain commits before rerun", result.Failed, result.Uncertain)
		}
		return nil
	case runtimeModeSeedCore, runtimeModeSeedPrivacy:
		return runSeedWithCreator(ctx, selection.Mode(), pool, blobs, handler.NewWithTitlePipeline(cfg, pool, blobs, titles))
	case runtimeModeServe:
		logTranscriptEncryptionAuthorityReady(keyring)
		return startHTTPServing(ctx, cfg, pool, blobs, titles)
	default:
		return errors.New("runtime dispatch failed because parsed mode is unknown in run after authority loading; no job or listener started; use a documented mode")
	}
}

func logTranscriptEncryptionAuthorityReady(keyring *config.TranscriptKeyring) {
	revision := os.Getenv("VILLAGE_BUILD_REVISION")
	if revision == "" {
		revision = "development"
	}
	slog.Info("transcript_encryption_authority_ready",
		"stage", "pre_listener",
		"active_key_version", keyring.ActiveVersion(),
		"revision", revision,
		"meaning", "serving authority and encrypted transcript storage are composed before listener startup")
}

func withTranscriptStorage(cfg *config.Config, keyring *config.TranscriptKeyring, start func(storage.TranscriptBlobStore) error) error {
	objects, err := constructObjectStore(cfg)
	if err != nil {
		return fmt.Errorf("runtime startup failed because object storage composition failed in withTranscriptStorage before mode dispatch; no job or listener started; correct the S3/AWS SDK configuration and retry: %w", err)
	}
	blobs, err := storage.NewEncryptedTranscriptStore(objects, keyring)
	if err != nil {
		return fmt.Errorf("runtime startup failed because encrypted storage composition failed in withTranscriptStorage before mode dispatch; no job or listener started; correct key custody and retry: %w", err)
	}
	return start(blobs)
}

func serve(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, titles *redact.TitlePipeline) error {
	srv := &http.Server{Addr: ":" + cfg.Port, Handler: router.New(cfg, pool, blobs, titles), ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP serving failed in serve after listener startup; requests are unavailable; repair the listener and restart: %w", err)
		}
		return nil
	case <-signals:
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// reportShareStateConsistency compares the derived transcript_shares projection
// against a latest-event fold over the whole ledger and reports the result. It
// writes NOTHING; repair is the separate, deliberate rebuild_transcript_shares().
//
// It exits NON-ZERO when the projection disagrees with the ledger, because a
// silent pass is worthless to CI and to an operator who ran it to find out.
//
// DEPLOYING THIS AS A ONE-SHOT JOB - read before scheduling it:
//   - Restart policy MUST be Never, and the job MUST have no public networking.
//     This platform reads a non-zero exit as a crash and restarts the service,
//     so a checker that correctly exits non-zero on drift would otherwise LOOP,
//     re-reporting the same drift forever. Clone the backend service, strip its
//     networking, set restart to Never.
//   - slog writes to stderr, so the platform tags ordinary INFO lines as errors.
//     A clean run can therefore LOOK like a failure in the log viewer. Read the
//     exit code, not the log colour.
func reportShareStateConsistency(ctx context.Context, pool *pgxpool.Pool) error {
	report, err := backfill.ShareStateConsistency(ctx, pool)
	if err != nil {
		return err
	}
	slog.Info("share_state_consistency_complete",
		"projection_rows", report.ProjectionRows,
		"ledger_pairs", report.LedgerPairs,
		"drift_rows", report.DriftRows,
		"consistent", report.Consistent())
	for _, row := range report.Sample {
		slog.Warn("share_state_drift",
			"transcript_id", uuid.UUID(row.TranscriptID.Bytes).String(),
			"group_id", uuid.UUID(row.GroupID.Bytes).String(),
			"problem", row.Problem,
			"stored_status", row.StoredStatus.String,
			"expected_status", row.ExpectedStatus.String)
	}
	if !report.Consistent() {
		return fmt.Errorf("share-state consistency found %d row(s) where the derived transcript_shares projection disagrees with a latest-event fold over transcript_share_attempts; nothing was changed, because this mode only reports; the ledger is authoritative, so repair by running SELECT rebuild_transcript_shares() in a maintenance window and rerun this check to confirm zero", report.DriftRows)
	}
	return nil
}
