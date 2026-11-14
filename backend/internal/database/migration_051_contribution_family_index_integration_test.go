//go:build integration

package database

import (
	"context"
	"strings"
	"testing"
)

// TestMigration051ContributionFamilyIndexLifecycle proves the index exists after
// the migrations with its columns in order, goes away with the down migration,
// is created again by re-running the up migration, and is chosen by the planner
// for the family expansion's owner and parent lookup.
func TestMigration051ContributionFamilyIndexLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)

	const indexName = "idx_transcripts_owner_parent_session"
	indexCount := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE tablename = 'transcripts' AND indexname = $1`, indexName).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", indexName, err)
		}
		return n
	}

	var definition string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE tablename = 'transcripts' AND indexname = $1`, indexName).Scan(&definition); err != nil {
		t.Fatalf("the index must exist after the migrations: %v", err)
	}
	// The column list in its order: the owner leads so the pair is one range.
	if !strings.Contains(definition, "USING btree (owner_id, parent_session_id)") {
		t.Fatalf("index definition = %q, want a btree on (owner_id, parent_session_id)", definition)
	}

	up, err := migrationsFS.ReadFile("migrations/051_contribution_family_index.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/051_contribution_family_index.down.sql")
	if err != nil {
		t.Fatal(err)
	}

	// The planner question: with sequential scans off, the family expansion's
	// owner and parent lookup must plan onto the new index.
	plan := func() string {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin explain: %v", err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
			t.Fatalf("disable sequential scans for the explain: %v", err)
		}
		rows, err := tx.Query(ctx, `
			EXPLAIN SELECT id FROM transcripts
			WHERE owner_id = $1 AND parent_session_id = $2
		`, "00000000-0000-0000-0000-000000000951", "family-root")
		if err != nil {
			t.Fatalf("explain the family expansion lookup: %v", err)
		}
		var b strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatalf("scan the plan: %v", err)
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("read the plan: %v", err)
		}
		return b.String()
	}
	if p := plan(); !strings.Contains(p, indexName) {
		t.Fatalf("the family expansion lookup does not plan onto the index; plan=%s", p)
	}

	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("reverse migration 051: %v", err)
	}
	if n := indexCount(); n != 0 {
		t.Fatalf("after the down migration the index count = %d, want 0", n)
	}

	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("re-run migration 051 up after down: %v", err)
	}
	if n := indexCount(); n != 1 {
		t.Fatalf("after re-running the up migration the index count = %d, want 1", n)
	}
	if p := plan(); !strings.Contains(p, indexName) {
		t.Fatalf("after re-running the up migration the lookup does not plan onto the index; plan=%s", p)
	}
}
