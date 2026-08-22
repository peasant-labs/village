package backfill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/handler"
	"github.com/peasant-labs/village/backend/internal/storage"
)

const DefaultTitleBackfillBatchSize = 100

type TitleBackfillMode uint8

const (
	TitleBackfillModeInvalid TitleBackfillMode = iota
	TitleBackfillModeDryRun
	TitleBackfillModeApply
)

func ParseTitleBackfillMode(value string) (TitleBackfillMode, error) {
	switch value {
	case "dry-run":
		return TitleBackfillModeDryRun, nil
	case "apply":
		return TitleBackfillModeApply, nil
	default:
		return TitleBackfillModeInvalid, errors.New("title backfill mode parsing failed because the supplied value is not one of dry-run or apply in backfill.ParseTitleBackfillMode before database or object storage access; the value was not echoed, no rows were scanned or changed, and no authority was accessed; choose exactly dry-run or apply")
	}
}

func (m TitleBackfillMode) String() string {
	switch m {
	case TitleBackfillModeDryRun:
		return "dry-run"
	case TitleBackfillModeApply:
		return "apply"
	default:
		return "invalid"
	}
}

type TitleBackfillResult struct {
	Scanned, Unchanged, WouldUpdate, Updated, Derived, Sanitized, Failed int
}

func (r TitleBackfillResult) Err() error {
	if r.Failed == 0 {
		return nil
	}
	return fmt.Errorf("title backfill completed after scanning %d rows but %d row failures left those rows unchanged; successful decisions remain valid; inspect the safe stage logs, restore the failed dependency or content, and rerun the same mode", r.Scanned, r.Failed)
}

type titlePipeline interface {
	Generate(string, redact.TitleContext) (redact.TitleResult, error)
	Sanitize(string, redact.TitleContext) (redact.TitleResult, error)
	GenerateFromTurns(turns []string, context redact.TitleContext) (redact.TitleResult, int, []error)
}

type contentMigrator interface {
	Migrate(context.Context, []byte) (*schema.SessionDetailPayload, bool, error)
}

type TitleBackfill struct {
	pool      *pgxpool.Pool
	blobs     storage.TranscriptBlobStore
	pipeline  titlePipeline
	migrator  contentMigrator
	logger    *slog.Logger
	batchSize int32
}

func NewTitleBackfill(pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, pipeline *redact.TitlePipeline, logger *slog.Logger, batchSize int) (*TitleBackfill, error) {
	if pool == nil || blobs == nil || pipeline == nil {
		return nil, errors.New("title backfill construction failed because PostgreSQL, encrypted blob storage, and the title pipeline are all required in backfill.NewTitleBackfill before scanning; no rows changed; inject every production dependency and retry")
	}
	if batchSize < 1 || batchSize > 1000 {
		return nil, fmt.Errorf("title backfill construction failed because batch size %d is outside 1..1000 in backfill.NewTitleBackfill before scanning; no rows changed; choose a bounded batch size and retry", batchSize)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &TitleBackfill{pool: pool, blobs: blobs, pipeline: pipeline, migrator: handler.NewContentMigrator(), logger: logger, batchSize: int32(batchSize)}, nil
}

func (b *TitleBackfill) Run(ctx context.Context, mode TitleBackfillMode) (TitleBackfillResult, error) {
	if mode != TitleBackfillModeDryRun && mode != TitleBackfillModeApply {
		return TitleBackfillResult{}, errors.New("title backfill execution failed because the mode is not dry-run or apply in backfill.TitleBackfill.Run before scanning; no rows changed; parse the operator value with ParseTitleBackfillMode")
	}
	q := sqlc.New(b.pool)
	var result TitleBackfillResult
	after := pgtype.UUID{Bytes: uuid.Nil, Valid: true}
	for {
		rows, err := q.ListTranscriptTitleBackfillBatch(ctx, sqlc.ListTranscriptTitleBackfillBatchParams{AfterID: after, BatchSize: b.batchSize})
		if err != nil {
			return result, fmt.Errorf("title backfill enumeration failed because PostgreSQL could not list the next keyset batch in backfill.TitleBackfill.Run; completed rows remain settled and no unscanned row changed; restore PostgreSQL connectivity and rerun: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			after = row.ID
			result.Scanned++
			decision, err := b.decide(ctx, row)
			if err != nil {
				result.Failed++
				b.logFailure(row.ID, "decision", err)
				continue
			}
			if decision.derived {
				result.Derived++
			}
			if decision.sanitized {
				result.Sanitized++
			}
			if sameText(row.Title, decision.title) && sameText(row.TitleGenerated, decision.generated) {
				result.Unchanged++
				continue
			}
			result.WouldUpdate++
			if mode == TitleBackfillModeDryRun {
				continue
			}
			updated, err := b.update(ctx, row, decision)
			if err != nil {
				result.Failed++
				b.logFailure(row.ID, "compare-and-set", err)
				continue
			}
			if updated {
				result.Updated++
			}
		}
	}
	return result, result.Err()
}

type titleDecision struct {
	title, generated   pgtype.Text
	derived, sanitized bool
}

func (b *TitleBackfill) decide(ctx context.Context, row sqlc.ListTranscriptTitleBackfillBatchRow) (titleDecision, error) {
	context := redact.TitleContext{Harness: canonicalBackfillHarness(row.ModelProvider), ProjectPath: row.ProjectPath.String}
	title, titleChanged, err := sanitizeStoredTitle(b.pipeline, row.Title, context)
	if err != nil {
		return titleDecision{}, fmt.Errorf("visible title sanitation failed: %w", err)
	}
	generated, generatedChanged, err := sanitizeStoredTitle(b.pipeline, row.TitleGenerated, context)
	if err != nil {
		return titleDecision{}, fmt.Errorf("generated title sanitation failed: %w", err)
	}
	decision := titleDecision{title: title, generated: generated, sanitized: titleChanged || generatedChanged}
	if title.Valid && !isGenericTitle(title.String, context.Harness) {
		return decision, nil
	}
	if generated.Valid && generated.String != "" && !isGenericTitle(generated.String, context.Harness) {
		decision.title = generated
		return decision, nil
	}
	derived, err := b.derive(ctx, row, context)
	if err != nil {
		return titleDecision{}, err
	}
	decision.title = textValue(derived)
	decision.generated = textValue(derived)
	decision.derived = true
	return decision, nil
}

func sanitizeStoredTitle(p titlePipeline, value pgtype.Text, context redact.TitleContext) (pgtype.Text, bool, error) {
	if !value.Valid || value.String == "" {
		return value, false, nil
	}
	result, err := p.Sanitize(value.String, context)
	if err != nil {
		return pgtype.Text{}, false, err
	}
	return textValue(result.Text), result.Text != value.String, nil
}

func (b *TitleBackfill) derive(ctx context.Context, row sqlc.ListTranscriptTitleBackfillBatchRow, titleContext redact.TitleContext) (string, error) {
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
	return deriveTitleFromPayload(payload, b.pipeline, titleContext, b.logger)
}

// deriveTitleFromPayload selects a title from the ordered RoleUser turns of an
// already-loaded and already-migrated payload. It has no pool or blob
// dependency, so it is directly unit-testable against fixture payloads.
//
// It collects the non-empty RoleUser turn contents in payload order and hands
// them to redact.GenerateFromTurns, the single canonical implementation of the
// skip-empty/skip-error turn-selection contract: a turn that cleans to empty
// text carries no user prose (for example a whole turn injected by the harness
// as a caveat or command-scaffolding block) and a turn whose recognized markup
// cannot be cleaned deterministically is unusable. Both are skipped in favor of
// the next candidate turn.
//
// Every skipped-with-error turn is logged at warn with its original payload
// turn index so an operator can locate the offending recorded turn. Because
// GenerateFromTurns reports only the aggregate list of per-turn errors (not
// which original turn produced each one), logSkippedTitleTurns re-walks the
// same candidate turns through the same pure Generate call to attribute each
// error back to its original index; this re-walk never changes which title
// wins, since the winning title and its index still come only from the single
// GenerateFromTurns call.
func deriveTitleFromPayload(payload *schema.SessionDetailPayload, pipeline titlePipeline, titleContext redact.TitleContext, logger *slog.Logger) (string, error) {
	var turns []string
	var indices []int
	for i, turn := range payload.Turns {
		if turn.Role != schema.RoleUser || strings.TrimSpace(turn.Content) == "" {
			continue
		}
		turns = append(turns, turn.Content)
		indices = append(indices, i)
	}
	result, winIndex, skipped := pipeline.GenerateFromTurns(turns, titleContext)
	if len(skipped) > 0 {
		logSkippedTitleTurns(pipeline, turns, indices, winIndex, titleContext, logger)
	}
	if winIndex == -1 {
		return "", errors.New("title derivation failed because no usable user turn remained after cleaning; every recorded user turn was either empty once harness-injected markup (system reminders, command scaffolding, tool output) was removed, or its markup could not be cleaned deterministically; the row remains unchanged; restore a usable user turn or set a safe manual title and retry")
	}
	return result.Text, nil
}

// logSkippedTitleTurns logs one warn line per turn that GenerateFromTurns
// could not clean deterministically, naming the original payload turn index.
// It never logs raw turn content; the underlying redact error text does not
// carry the raw turn text either.
func logSkippedTitleTurns(pipeline titlePipeline, turns []string, indices []int, winIndex int, titleContext redact.TitleContext, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	end := len(turns)
	if winIndex >= 0 {
		end = winIndex
	}
	for i := 0; i < end; i++ {
		if _, err := pipeline.Generate(turns[i], titleContext); err != nil {
			logger.Warn("title backfill skipped a candidate user turn because its recognized harness markup could not be cleaned deterministically; the pipeline tried the next user turn", "turn_index", indices[i], "cause_type", fmt.Sprintf("%T", err))
		}
	}
}

func (b *TitleBackfill) update(ctx context.Context, row sqlc.ListTranscriptTitleBackfillBatchRow, decision titleDecision) (bool, error) {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackMaintenanceTransaction(tx, "title_backfill_update")
	if _, err = tx.Exec(ctx, "SELECT set_config('app.actor_id',$1,true)", database.SystemActorID); err != nil {
		return false, err
	}
	n, err := sqlc.New(tx).CompareAndSwapTranscriptTitles(ctx, sqlc.CompareAndSwapTranscriptTitlesParams{Title: decision.title, TitleGenerated: decision.generated, ID: row.ID, ExpectedTitle: row.Title, ExpectedTitleGenerated: row.TitleGenerated, ExpectedUpdatedAt: row.UpdatedAt})
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return n == 1, nil
}

func (b *TitleBackfill) logFailure(id pgtype.UUID, stage string, err error) {
	b.logger.Error("title backfill row failed; row remains unchanged; correct the dependency or stored content identified by the stage and rerun", "transcript_id", uuid.UUID(id.Bytes).String(), "stage", stage, "impact", "row unchanged", "recovery", "repair the failed stage and rerun", "cause_type", fmt.Sprintf("%T", err))
}

func canonicalBackfillHarness(value string) schema.Harness {
	switch value {
	case "claude":
		return schema.HarnessClaudeCode
	case "gemini":
		return schema.HarnessGeminiCLI
	default:
		return schema.Harness(value)
	}
}

func isGenericTitle(value string, harness schema.Harness) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "session" || value == "model session" || value == "transcript" {
		return true
	}
	return value == strings.ToLower(string(harness))+" session" || value == genericHarnessName(harness)+" session"
}

func genericHarnessName(harness schema.Harness) string {
	switch harness {
	case schema.HarnessClaudeCode:
		return "claude"
	case schema.HarnessGeminiCLI:
		return "gemini"
	default:
		return strings.ReplaceAll(string(harness), "-", " ")
	}
}

func sameText(a, b pgtype.Text) bool     { return a.Valid == b.Valid && (!a.Valid || a.String == b.String) }
func textValue(value string) pgtype.Text { return pgtype.Text{String: value, Valid: value != ""} }
func nullableString(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
func nullableInt64(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
