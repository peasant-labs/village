//go:build integration

package database

// Integration test for migration 024 — adds transcripts.content_hash (nullable).
//
// Requires a running PostgreSQL instance. Run with:
//
//	TEST_DATABASE_URL="postgres://peasant:peasant@localhost:5432/peasant_test?sslmode=disable" \
//	  go test -tags=integration ./internal/database/...
//
// Mutations run inside a transaction rolled back on cleanup, so the test is
// hermetic.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration024_AppliesContentHashColumn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, migration023TestDatabaseURL(t))
	if err != nil {
		t.Skipf("no test database available: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("test database not reachable: %v", err)
	}

	migrateTestDatabaseThrough(t, pool, migrationBoundary024)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx)

	// The column must exist and be nullable.
	var isNullable string
	err = tx.QueryRow(ctx, `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_name = 'transcripts' AND column_name = 'content_hash'
	`).Scan(&isNullable)
	if err != nil {
		t.Fatalf("content_hash column missing after migration 024: %v", err)
	}
	if isNullable != "YES" {
		t.Fatalf("content_hash is_nullable = %q, want YES", isNullable)
	}

	// A fresh transcript starts with a NULL content_hash; it can be SET and read back.
	ownerID := insertUserForContentHash(t, ctx, tx)
	tid := insertTranscript(t, ctx, tx, ownerID, "local-024", "claude-code", "transcripts/x/y/transcript.json")

	var nullHash *string
	if err := tx.QueryRow(ctx, "SELECT content_hash FROM transcripts WHERE id = $1", tid).Scan(&nullHash); err != nil {
		t.Fatalf("read initial content_hash: %v", err)
	}
	if nullHash != nil {
		t.Fatalf("new transcript content_hash = %q, want NULL", *nullHash)
	}

	const want = "deadbeef"
	if _, err := tx.Exec(ctx, "UPDATE transcripts SET content_hash = $2 WHERE id = $1", tid, want); err != nil {
		t.Fatalf("set content_hash: %v", err)
	}
	var got *string
	if err := tx.QueryRow(ctx, "SELECT content_hash FROM transcripts WHERE id = $1", tid).Scan(&got); err != nil {
		t.Fatalf("read content_hash: %v", err)
	}
	if got == nil || *got != want {
		t.Fatalf("content_hash = %v, want %q", got, want)
	}
}

func insertUserForContentHash(t *testing.T, ctx context.Context, tx pgx.Tx) string {
	t.Helper()
	// provider_user_id is NOT NULL (migration 015); github_id is BIGINT UNIQUE.
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES ($1, $2, $3) RETURNING id::text
	`, int64(924001), "user024", "924001").Scan(&id)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}
