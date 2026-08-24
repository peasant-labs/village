package backfill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/handler"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
	"github.com/peasant-labs/village/backend/internal/storage"
)

const DefaultOriginBackfillBatchSize = 100

// OriginBackfillMode is the closed set of operator intents. Dry-run reads and
// decides but writes nothing; apply performs the compare-and-set.
type OriginBackfillMode uint8

const (
	OriginBackfillModeInvalid OriginBackfillMode = iota
	OriginBackfillModeDryRun
	OriginBackfillModeApply
)

func ParseOriginBackfillMode(value string) (OriginBackfillMode, error) {
	switch value {
	case "dry-run":
		return OriginBackfillModeDryRun, nil
	case "apply":
		return OriginBackfillModeApply, nil
	default:
		return OriginBackfillModeInvalid, errors.New("origin backfill mode parsing failed because the supplied value is not one of dry-run or apply in backfill.ParseOriginBackfillMode before database or object storage access; the value was not echoed, no rows were scanned or changed, and no authority was accessed; choose exactly dry-run or apply")
	}
}

func (m OriginBackfillMode) String() string {
	switch m {
	case OriginBackfillModeDryRun:
		return "dry-run"
	case OriginBackfillModeApply:
		return "apply"
	default:
		return "invalid"
	}
}

type OriginBackfillResult struct {
	Scanned, Unchanged, WouldUpdate, Updated, Failed int
}

func (r OriginBackfillResult) Err() error {
	if r.Failed == 0 {
		return nil
	}
	return fmt.Errorf("origin backfill completed after scanning %d rows but %d row failures left those rows unchanged; successful decisions remain valid; inspect the safe stage logs, restore the failed dependency or content, and rerun the same mode", r.Scanned, r.Failed)
}

// OriginBackfill reclassifies stored transcripts so rows published before the
// session-origin column existed stop occupying a publisher's root-level list.
// It reuses the publish-path classifier, so a backfilled row and a freshly
// published one can never disagree.
type OriginBackfill struct {
	pool      *pgxpool.Pool
	blobs     storage.TranscriptBlobStore
	migrator  contentMigrator
	logger    *slog.Logger
	batchSize int32
}

func NewOriginBackfill(pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, logger *slog.Logger, batchSize int) (*OriginBackfill, error) {
	if pool == nil || blobs == nil {
		return nil, errors.New("origin backfill construction failed because PostgreSQL and encrypted blob storage are both required in backfill.NewOriginBackfill before scanning; no rows changed; inject every production dependency and retry")
	}
	if batchSize < 1 || batchSize > 1000 {
		return nil, fmt.Errorf("origin backfill construction failed because batch size %d is outside 1..1000 in backfill.NewOriginBackfill before scanning; no rows changed; choose a bounded batch size and retry", batchSize)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &OriginBackfill{pool: pool, blobs: blobs, migrator: handler.NewContentMigrator(), logger: logger, batchSize: int32(batchSize)}, nil
}

func (b *OriginBackfill) Run(ctx context.Context, mode OriginBackfillMode) (OriginBackfillResult, error) {
	if mode != OriginBackfillModeDryRun && mode != OriginBackfillModeApply {
		return OriginBackfillResult{}, errors.New("origin backfill execution failed because the mode is not dry-run or apply in backfill.OriginBackfill.Run before scanning; no rows changed; parse the operator value with ParseOriginBackfillMode")
	}
	q := sqlc.New(b.pool)
	var result OriginBackfillResult
	after := pgtype.UUID{Bytes: uuid.Nil, Valid: true}
	for {
		rows, err := q.ListTranscriptOriginBackfillBatch(ctx, sqlc.ListTranscriptOriginBackfillBatchParams{AfterID: after, BatchSize: b.batchSize})
		if err != nil {
			return result, fmt.Errorf("origin backfill enumeration failed because PostgreSQL could not list the next keyset batch in backfill.OriginBackfill.Run; completed rows remain settled and no unscanned row changed; restore PostgreSQL connectivity and rerun: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			after = row.ID
			result.Scanned++
			decided, err := b.decide(ctx, row)
			if err != nil {
				result.Failed++
				b.logOriginFailure(row.ID, "decision", err)
				continue
			}
			if decided.String() == row.SessionOrigin {
				result.Unchanged++
				continue
			}
			result.WouldUpdate++
			if mode == OriginBackfillModeDryRun {
				continue
			}
			updated, err := b.update(ctx, row, decided)
			if err != nil {
				result.Failed++
				b.logOriginFailure(row.ID, "compare-and-set", err)
				continue
			}
			if updated {
				result.Updated++
			}
		}
	}
	return result, result.Err()
}

// decide reads the stored content and classifies it. The value already in the
// column is validated first: a value outside the menu means the row was written
// by something that bypassed the application, so the run fails that row instead
// of overwriting evidence of the bypass.
func (b *OriginBackfill) decide(ctx context.Context, row sqlc.ListTranscriptOriginBackfillBatchRow) (sessionorigin.Origin, error) {
	if err := sessionorigin.Origin(row.SessionOrigin).Validate(); err != nil {
		return "", fmt.Errorf("stored session origin rejected before reclassification: %w", err)
	}
	descriptor, err := storage.NewBlobDescriptor(storage.ObjectKey(row.BlobKey), row.WrappedDataKey, storage.EncryptionAlgorithm(row.EncryptionAlgorithm), storage.KeyVersion(row.KeyVersion))
	if err != nil {
		return "", fmt.Errorf("encrypted descriptor validation failed; repair the row descriptor and retry: %w", err)
	}
	loaded, err := storage.NewLoadedContentIdentity(nullableString(row.ContentHash), nullableInt64(row.BlobSizeBytes))
	if err != nil {
		return "", fmt.Errorf("content identity validation failed; repair the stored identity and retry: %w", err)
	}
	raw, _, err := b.blobs.Read(ctx, uuid.UUID(row.ID.Bytes), descriptor, loaded)
	if err != nil {
		return "", fmt.Errorf("authenticated blob read failed; restore object or key authority and retry: %w", err)
	}
	payload, _, err := b.migrator.Migrate(ctx, raw)
	if err != nil {
		return "", fmt.Errorf("stored content normalization failed; repair the current or legacy content and retry: %w", err)
	}
	return sessionorigin.Classify(payload), nil
}

// update installs the decision under a compare-and-set on the origin the row
// carried when it was read, so a concurrent publish that already reclassified
// the row wins and this run reports no update rather than clobbering it.
func (b *OriginBackfill) update(ctx context.Context, row sqlc.ListTranscriptOriginBackfillBatchRow, decided sessionorigin.Origin) (bool, error) {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackMaintenanceTransaction(tx, "origin_backfill_update")
	if _, err = tx.Exec(ctx, "SELECT set_config('app.actor_id',$1,true)", database.SystemActorID); err != nil {
		return false, err
	}
	n, err := sqlc.New(tx).CompareAndSwapTranscriptSessionOrigin(ctx, sqlc.CompareAndSwapTranscriptSessionOriginParams{
		SessionOrigin:         decided.String(),
		ID:                    row.ID,
		ExpectedSessionOrigin: row.SessionOrigin,
		ExpectedUpdatedAt:     row.UpdatedAt,
	})
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return n == 1, nil
}

func (b *OriginBackfill) logOriginFailure(id pgtype.UUID, stage string, err error) {
	b.logger.Error("origin backfill row failed; row remains unchanged and stays fully visible; correct the dependency or stored content identified by the stage and rerun", "transcript_id", uuid.UUID(id.Bytes).String(), "stage", stage, "impact", "row unchanged", "recovery", "repair the failed stage and rerun", "cause_type", fmt.Sprintf("%T", err))
}
