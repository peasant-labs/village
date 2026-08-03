//go:build integration

package handler

// Integration test for commit-batch ATOMICITY: inTxAs + persistCommits
// wraps the prune DELETE + batched INSERT in a single transaction, so a
// partial failure can never leave a half-written commit set.
//
// This path CANNOT be unit-tested with a mock Querier: h.pool is a concrete
// *pgxpool.Pool, not behind the Querier interface, and the atomicity guarantee
// lives in the transaction itself. It therefore requires a real PostgreSQL
// instance. Run with:
//
//	TEST_DATABASE_URL="postgres://peasant:peasant@localhost:5432/peasant_test?sslmode=disable" \
//	  go test -tags=integration ./internal/handler/...
//
// State is cleaned up by deleting the owning user on completion (FK ON DELETE
// CASCADE removes the transcript and its commits), so the test is hermetic.

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func atomicityTestDatabaseURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return "postgres://peasant:peasant@localhost:5432/peasant_test?sslmode=disable"
}

// readCommitSHAs returns the sorted SHA set currently stored for a transcript.
func readCommitSHAs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, transcriptID pgtype.UUID) []string {
	t.Helper()
	rows, err := pool.Query(ctx, "SELECT sha FROM transcript_commits WHERE transcript_id = $1", transcriptID)
	if err != nil {
		t.Fatalf("read commit shas: %v", err)
	}
	defer rows.Close()
	var shas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan sha: %v", err)
		}
		shas = append(shas, s)
	}
	sort.Strings(shas)
	return shas
}

func TestPersistCommits_InTxAs_RollsBackOnPartialFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, atomicityTestDatabaseURL(t))
	if err != nil {
		t.Skipf("cannot create pool (%v); set TEST_DATABASE_URL", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("cannot reach test database (%v); set TEST_DATABASE_URL", err)
	}
	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Owner (FK target). provider defaults to 'github'; provider_user_id is NOT
	// NULL (migration 015). Cleaned up via cascade on the user delete.
	var ownerID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES ($1, $2, $3) RETURNING id
	`, int64(690001), "atomicity-owner", "690001").Scan(&ownerID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	defer cleanupOwners(t, ctx, pool, ownerID)

	// System-actor fixture insert (the migration-026 publish trigger is fail-closed).
	transcriptID := pullInsertTranscript(t, ctx, pool, ownerID, "atomicity-local", "private")

	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	// Seed a known-good commit set {a, b}.
	seed := []schema.CommitInfo{
		{Hash: "a", Message: "first", AuthorTime: 1, CommitTime: 2},
		{Hash: "b", Message: "second", AuthorTime: 3, CommitTime: 4},
	}
	if err := h.inTxAs(ctx, ownerID, func(q Querier) error { return persistCommits(ctx, q, transcriptID, seed) }); err != nil {
		t.Fatalf("seed persistCommits: %v", err)
	}
	if got := readCommitSHAs(t, ctx, pool, transcriptID); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("after seed: got %v, want [a b]", got)
	}

	// Force a partial failure DETERMINISTICALLY: install a CHECK constraint that
	// rejects a poison SHA, then persist a payload containing it. The prune
	// DELETE runs first inside the inTxAs tx; the INSERT then violates
	// the CHECK. If the wrapper is atomic, the rollback must restore {a, b}.
	// (A duplicate-SHA payload can no longer serve as the trigger — persistCommits
	// now dedups by SHA before the batched insert.)
	if _, err := pool.Exec(ctx,
		`ALTER TABLE transcript_commits ADD CONSTRAINT atomicity_reject_poison CHECK (sha <> 'POISON')`); err != nil {
		t.Fatalf("install poison CHECK: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `ALTER TABLE transcript_commits DROP CONSTRAINT IF EXISTS atomicity_reject_poison`)
	}()

	bad := []schema.CommitInfo{
		{Hash: "a", Message: "kept", AuthorTime: 5, CommitTime: 6},
		{Hash: "POISON", Message: "boom", AuthorTime: 7, CommitTime: 8},
	}
	if err := h.inTxAs(ctx, ownerID, func(q Querier) error { return persistCommits(ctx, q, transcriptID, bad) }); err == nil {
		t.Fatal("expected the atomic persist to fail when the INSERT violates the poison CHECK")
	}

	// ATOMICITY: the failed call must NOT have dropped the prior set. A non-atomic
	// implementation (DELETE outside the tx) would leave 0 rows here.
	if got := readCommitSHAs(t, ctx, pool, transcriptID); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("after failed persist: got %v, want [a b] (DELETE must have rolled back)", got)
	}

	// Happy-path shrink: replacing {a,b} with {a} commits and prunes b.
	if err := h.inTxAs(ctx, ownerID, func(q Querier) error {
		return persistCommits(ctx, q, transcriptID, []schema.CommitInfo{
			{Hash: "a", Message: "first", AuthorTime: 1, CommitTime: 2},
		})
	}); err != nil {
		t.Fatalf("shrink persistCommits: %v", err)
	}
	if got := readCommitSHAs(t, ctx, pool, transcriptID); len(got) != 1 || got[0] != "a" {
		t.Fatalf("after shrink: got %v, want [a] (b pruned)", got)
	}
}
