//go:build integration

package database

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func migration030UpSQL(t *testing.T) string {
	t.Helper()
	up, err := migrationsFS.ReadFile("migrations/030_manual_annotation_transcript_targets.up.sql")
	if err != nil {
		t.Fatalf("read migration 030 up: %v", err)
	}
	return string(up)
}

func TestMigration030_ManualAnnotationTargetCatalogAndBackfill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping manual annotation target migration integration test in -short mode")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, migration023TestDatabaseURL(t))
	if err != nil {
		t.Skipf("cannot create database pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("cannot reach test database: %v", err)
	}
	migrateTestDatabaseThrough(t, pool, migrationBoundary030)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin migration 030 transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	requireMigration030NullableColumn(t, ctx, tx)
	requireCatalogConstraint(t, ctx, tx, "annotations_target_transcript_id_fk", "manual annotation transcript FK", "FOREIGN KEY (target_transcript_id)", "REFERENCES transcripts(id)", "ON DELETE CASCADE")
	requireCatalogIndex(t, ctx, tx, "idx_annotations_target_transcript")

	seed := time.Now().UnixNano()
	transcriptOwner := insertMigration030User(t, ctx, tx, seed+1, "manual-target-owner")
	viewer := insertMigration030User(t, ctx, tx, seed+2, "manual-target-viewer")
	duplicateOwner := insertMigration030User(t, ctx, tx, seed+3, "manual-target-duplicate-owner")
	transcriptID := insertTranscript(t, ctx, tx, transcriptOwner, "migration-030-local", "claude-code", "blob/migration-030")
	insertTranscript(t, ctx, tx, transcriptOwner, "migration-030-duplicate", "claude-code", "blob/migration-030-duplicate-a")
	insertTranscript(t, ctx, tx, duplicateOwner, "migration-030-duplicate", "claude-code", "blob/migration-030-duplicate-b")

	legacyManualID := insertMigration030Annotation(t, ctx, tx, "migration-030-legacy-manual", viewer, "entry", "migration-030-local", "human")
	ambiguousManualID := insertMigration030Annotation(t, ctx, tx, "migration-030-ambiguous-manual", viewer, "entry", "migration-030-duplicate", "human")
	unresolvedManualID := insertMigration030Annotation(t, ctx, tx, "migration-030-unresolved-manual", transcriptOwner, "entry", "missing-legacy-local", "human")
	pushedID := insertMigration030Annotation(t, ctx, tx, "migration-030-pushed-control", transcriptOwner, "entry", "migration-030-local", "")
	nonHumanID := insertMigration030Annotation(t, ctx, tx, "migration-030-non-human-control", viewer, "entry", "migration-030-local", "agent")

	// Re-running the guarded source against a current catalog executes the exact
	// shipped backfill after fixtures simulate rows that predate migration 030.
	if _, err := tx.Exec(ctx, migration030UpSQL(t)); err != nil {
		t.Fatalf("re-apply migration 030 source for backfill proof: %v", err)
	}
	if got := migration030AnnotationTarget(t, ctx, tx, legacyManualID); got == nil || *got != transcriptID {
		t.Fatalf("viewer-owned legacy human manual target=%v, want globally uniquely resolved transcript %q", got, transcriptID)
	}
	if got := migration030AnnotationTarget(t, ctx, tx, ambiguousManualID); got != nil {
		t.Fatalf("ambiguous legacy human manual target=%v, want NULL", *got)
	}
	if got := migration030AnnotationTarget(t, ctx, tx, unresolvedManualID); got != nil {
		t.Fatalf("unresolved legacy human manual target=%v, want NULL", *got)
	}
	if got := migration030AnnotationTarget(t, ctx, tx, pushedID); got != nil {
		t.Fatalf("pushed annotation target=%v, want NULL because the Village-only backfill must not rewrite wire rows", *got)
	}
	if got := migration030AnnotationTarget(t, ctx, tx, nonHumanID); got != nil {
		t.Fatalf("non-human annotation target=%v, want NULL because the Village-only backfill is human-only", *got)
	}
	if _, err := tx.Exec(ctx, migration030UpSQL(t)); err != nil {
		t.Fatalf("re-apply migration 030 source idempotence: %v", err)
	}
	if got := migration030AnnotationTarget(t, ctx, tx, legacyManualID); got == nil || *got != transcriptID {
		t.Fatalf("idempotent legacy backfill target=%v, want %q", got, transcriptID)
	}
	if got := migration030AnnotationTarget(t, ctx, tx, ambiguousManualID); got != nil {
		t.Fatalf("idempotent ambiguous legacy backfill target=%v, want NULL", *got)
	}

	// A viewer may legally attach a manual label to another owner's transcript;
	// only the exact transcript FK is required. A nonexistent UUID must still fail.
	if _, err := tx.Exec(ctx, `
		INSERT INTO annotations (content_hash, owner_id, target_kind, entry_session_id, entry_index, entry_end_index, target_transcript_id, type_id, value, annotator_kind)
		VALUES ($1, $2::uuid, 'entry', $3, 1, 2, $4::uuid, 'catalog.type', 'value', 'human')
	`, "migration-030-cross-owner-valid", viewer, "migration-030-local", transcriptID); err != nil {
		t.Fatalf("insert cross-owner manual target: %v", err)
	}
	requireCatalogMutationFailure(t, ctx, tx, "manual target transcript FK", `
		INSERT INTO annotations (content_hash, owner_id, target_kind, entry_session_id, entry_index, entry_end_index, target_transcript_id, type_id, value, annotator_kind)
		VALUES ($1, $2::uuid, 'entry', 'missing-target', 1, 2, $3::uuid, 'catalog.type', 'value', 'human')
	`, "migration-030-invalid-target", viewer, uuid.NewString())

	if _, err := tx.Exec(ctx, "DELETE FROM transcripts WHERE id = $1::uuid", transcriptID); err != nil {
		t.Fatalf("delete target transcript for FK cascade proof: %v", err)
	}
	var cascaded int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM annotations WHERE target_transcript_id = $1::uuid`, transcriptID).Scan(&cascaded); err != nil {
		t.Fatalf("count cascaded manual labels: %v", err)
	}
	if cascaded != 0 {
		t.Fatalf("manual target FK cascade left %d rows, want 0", cascaded)
	}
}

func requireMigration030NullableColumn(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()
	var nullable string
	if err := tx.QueryRow(ctx, `
		SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'annotations' AND column_name = 'target_transcript_id'
	`).Scan(&nullable); err != nil {
		t.Fatalf("read manual target column catalog: %v", err)
	}
	if nullable != "YES" {
		t.Fatalf("annotations.target_transcript_id nullable=%q, want YES", nullable)
	}
}

func insertMigration030User(t *testing.T, ctx context.Context, tx pgx.Tx, githubID int64, username string) string {
	t.Helper()
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, githubID, username, username).Scan(&id); err != nil {
		t.Fatalf("insert migration 030 user %q: %v", username, err)
	}
	return id
}

func insertMigration030Annotation(t *testing.T, ctx context.Context, tx pgx.Tx, contentHash, ownerID, targetKind, entrySessionID, annotatorKind string) string {
	t.Helper()
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO annotations (content_hash, owner_id, target_kind, entry_session_id, entry_index, entry_end_index, type_id, value, annotator_kind)
		VALUES ($1, $2::uuid, $3, $4, 1, 2, 'catalog.type', 'value', NULLIF($5, ''))
		RETURNING id::text
	`, contentHash, ownerID, targetKind, entrySessionID, annotatorKind).Scan(&id); err != nil {
		t.Fatalf("insert migration 030 annotation %q: %v", contentHash, err)
	}
	return id
}

func migration030AnnotationTarget(t *testing.T, ctx context.Context, tx pgx.Tx, annotationID string) *string {
	t.Helper()
	var target pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT target_transcript_id FROM annotations WHERE id = $1::uuid`, annotationID).Scan(&target); err != nil {
		t.Fatalf("read migration 030 annotation target %q: %v", annotationID, err)
	}
	if !target.Valid {
		return nil
	}
	value := uuid.UUID(target.Bytes).String()
	return &value
}
