//go:build integration

package database

// Integration test for migration 023 — the storage-normalization
// backfill of legacy harness VALUES (claude->claude-code, gemini->gemini-cli).
//
// Requires a running PostgreSQL instance. Run with:
//
//	TEST_DATABASE_URL="postgres://peasant:peasant@localhost:5432/peasant_test?sslmode=disable" \
//	  go test -tags=integration ./internal/database/...
//
// All mutations run inside a transaction that is rolled back on cleanup, so the
// test is hermetic and leaves no state behind.

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func migration023TestDatabaseURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return "postgres://peasant:peasant@localhost:5432/peasant_test?sslmode=disable"
}

// backfillUpSQL reads the real 023 up migration from the embedded FS, so the
// test exercises the exact SQL that ships — not a hand-copied reword.
func backfillUpSQL(t *testing.T) string {
	t.Helper()
	b, err := migrationsFS.ReadFile("migrations/023_backfill_harness_values.up.sql")
	if err != nil {
		t.Fatalf("read 023 up migration: %v", err)
	}
	return string(b)
}

// insertTranscript inserts one transcript row owned by ownerID and returns its id.
//
// It first declares the SYSTEM actor on the CALLER'S transaction (SET LOCAL
// semantics via set_config(..., is_local=true)): migration 026's governance-audit
// publish trigger is FAIL-CLOSED — any transcripts INSERT without app.actor_id
// aborts. The GUC must be set on the caller's tx, NOT a helper-owned txn: these
// migration tests run one rolled-back outer transaction for hermetic isolation,
// so a self-contained Begin/Commit here would leak fixture rows and FK-fail
// against the uncommitted owner. The GUC persists for the rest of the caller's
// tx, which is exactly what its fixtures want.
func insertTranscript(t *testing.T, ctx context.Context, tx pgx.Tx, ownerID, localID, modelProvider, blobKey string) string {
	t.Helper()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true)", SystemActorID); err != nil {
		t.Fatalf("declare system actor for transcript fixture: %v", err)
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO transcripts (owner_id, local_id, title, model_provider, model_name, blob_key, schema_version)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text
	`, ownerID, localID, "title-"+localID, modelProvider, "model-"+localID, blobKey, "2").Scan(&id)
	if err != nil {
		t.Fatalf("insert transcript (%s): %v", modelProvider, err)
	}
	return id
}

func countProvider(t *testing.T, ctx context.Context, tx pgx.Tx, value string) int {
	t.Helper()
	var n int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM transcripts WHERE model_provider = $1", value).Scan(&n); err != nil {
		t.Fatalf("count model_provider=%q: %v", value, err)
	}
	return n
}

func TestMigration023_BackfillHarnessValues(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, migration023TestDatabaseURL(t))
	if err != nil {
		t.Skipf("cannot create pool (%v); set TEST_DATABASE_URL", err)
	}
	defer pool.Close()

	// Stop at 023 so the legacy rows exercise this migration's historical shape.
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("cannot reach test database (%v); set TEST_DATABASE_URL", err)
	}
	migrateTestDatabaseThrough(t, pool, migrationBoundary023)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	// Roll back via defer (NOT t.Cleanup): the tx holds a pooled connection, and
	// the earlier-deferred pool.Close() blocks until that connection is released.
	// defer is LIFO, so this rollback runs before pool.Close() even on Fatalf.
	defer func() { _ = tx.Rollback(ctx) }()

	// Owner for the FK. provider defaults to 'github'; provider_user_id is
	// NOT NULL (migration 015) so it must be supplied. These are the OAuth
	// identity columns — unrelated to the harness; set here only to satisfy
	// the FK target's constraints.
	var ownerID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id) VALUES ($1, $2, $3) RETURNING id::text
	`, int64(909001), "m023-owner", "909001").Scan(&ownerID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	// Seed mis-stored legacy rows + already-canonical + non-legacy controls.
	insertTranscript(t, ctx, tx, ownerID, "claude-a", "claude", "blob/claude-a")
	insertTranscript(t, ctx, tx, ownerID, "claude-b", "claude", "blob/claude-b")
	insertTranscript(t, ctx, tx, ownerID, "gemini-a", "gemini", "blob/gemini-a")
	canonID := insertTranscript(t, ctx, tx, ownerID, "canon-a", "claude-code", "blob/canon-a")
	opencodeID := insertTranscript(t, ctx, tx, ownerID, "oc-a", "opencode", "blob/oc-a")

	// Capture content-shape columns for a control
	// row before the backfill, so we can prove the backfill touches only the
	// model_provider VALUE and never the blob/shape.
	type shape struct{ blobKey, schemaVersion, title, modelName, provider string }
	readShape := func(id string) shape {
		var s shape
		if err := tx.QueryRow(ctx, `
			SELECT blob_key, schema_version, COALESCE(title,''), COALESCE(model_name,''), model_provider
			FROM transcripts WHERE id = $1::uuid
		`, id).Scan(&s.blobKey, &s.schemaVersion, &s.title, &s.modelName, &s.provider); err != nil {
			t.Fatalf("read shape %s: %v", id, err)
		}
		return s
	}
	opencodeBefore := readShape(opencodeID)
	canonBefore := readShape(canonID)

	// --- Apply the real 023 backfill SQL ---
	upSQL := backfillUpSQL(t)
	if _, err := tx.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply 023 backfill: %v", err)
	}

	// Mapping assertions (>= 2 distinct mappings: claude->claude-code, gemini->gemini-cli).
	if got := countProvider(t, ctx, tx, "claude"); got != 0 {
		t.Errorf("legacy 'claude' rows after backfill: got %d, want 0", got)
	}
	if got := countProvider(t, ctx, tx, "gemini"); got != 0 {
		t.Errorf("legacy 'gemini' rows after backfill: got %d, want 0", got)
	}
	// 2 backfilled 'claude' + 1 originally-canonical 'claude-code' = 3.
	if got := countProvider(t, ctx, tx, "claude-code"); got != 3 {
		t.Errorf("'claude-code' rows after backfill: got %d, want 3", got)
	}
	if got := countProvider(t, ctx, tx, "gemini-cli"); got != 1 {
		t.Errorf("'gemini-cli' rows after backfill: got %d, want 1", got)
	}
	// Non-legacy control untouched.
	if got := countProvider(t, ctx, tx, "opencode"); got != 1 {
		t.Errorf("'opencode' rows after backfill: got %d, want 1", got)
	}

	// The backfill changed only model_provider, never the blob or shape.
	opencodeAfter := readShape(opencodeID)
	if opencodeAfter != opencodeBefore {
		t.Errorf("non-legacy row mutated by backfill: before=%+v after=%+v", opencodeBefore, opencodeAfter)
	}
	canonAfter := readShape(canonID)
	// The canonical row's provider was already 'claude-code' and must stay so;
	// all its shape columns must be unchanged.
	if canonAfter != canonBefore {
		t.Errorf("already-canonical row mutated by backfill: before=%+v after=%+v", canonBefore, canonAfter)
	}

	// --- Idempotence: re-running is a no-op ---
	if _, err := tx.Exec(ctx, upSQL); err != nil {
		t.Fatalf("re-apply 023 backfill: %v", err)
	}
	if got := countProvider(t, ctx, tx, "claude-code"); got != 3 {
		t.Errorf("idempotence: 'claude-code' rows after re-run: got %d, want 3", got)
	}
	if got := countProvider(t, ctx, tx, "gemini-cli"); got != 1 {
		t.Errorf("idempotence: 'gemini-cli' rows after re-run: got %d, want 1", got)
	}
	if got := countProvider(t, ctx, tx, "claude"); got != 0 {
		t.Errorf("idempotence: 'claude' rows after re-run: got %d, want 0", got)
	}
	if got := countProvider(t, ctx, tx, "opencode"); got != 1 {
		t.Errorf("idempotence: 'opencode' rows after re-run: got %d, want 1", got)
	}

	// --- Down migration runs clean and reverses the value mapping ---
	// (The down is intentionally lossy — see the .down.sql comment — so the
	// originally-canonical row reverts to 'claude' too: 3 canonical -> 3 legacy.)
	downBytes, err := migrationsFS.ReadFile("migrations/023_backfill_harness_values.down.sql")
	if err != nil {
		t.Fatalf("read 023 down migration: %v", err)
	}
	if _, err := tx.Exec(ctx, string(downBytes)); err != nil {
		t.Fatalf("apply 023 down: %v", err)
	}
	if got := countProvider(t, ctx, tx, "claude"); got != 3 {
		t.Errorf("down: 'claude' rows after revert: got %d, want 3", got)
	}
	if got := countProvider(t, ctx, tx, "gemini"); got != 1 {
		t.Errorf("down: 'gemini' rows after revert: got %d, want 1", got)
	}
	if got := countProvider(t, ctx, tx, "claude-code"); got != 0 {
		t.Errorf("down: 'claude-code' rows after revert: got %d, want 0", got)
	}
	if got := countProvider(t, ctx, tx, "opencode"); got != 1 {
		t.Errorf("down: non-legacy 'opencode' rows must be untouched: got %d, want 1", got)
	}
}
