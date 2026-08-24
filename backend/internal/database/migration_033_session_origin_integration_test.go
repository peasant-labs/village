//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/peasant-labs/village/backend/internal/sessionorigin"
)

// TestMigration033SessionOriginMenuAndDefault proves the real column over a
// real database: an existing row survives the migration and takes the
// fail-safe 'unknown' default, every menu member round-trips, and a value
// outside the menu is rejected by the CHECK rather than stored.
func TestMigration033SessionOriginMenuAndDefault(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	migrateTestDatabaseThrough(t, pool, migrationBoundary032)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := insertFenceOwner(t, ctx, tx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
		t.Fatal(err)
	}
	existingID, err := insertFenceTranscript(ctx, tx, ownerID)
	if err != nil {
		t.Fatalf("insert pre-033 transcript fixture: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit pre-033 fixture: %v", err)
	}

	if err := runMigration(pool, requireMigrationVersion(t, 33)); err != nil {
		t.Fatalf("apply exact migration 033 over the 032 boundary: %v", err)
	}

	var defaulted string
	if err := pool.QueryRow(ctx, `SELECT session_origin FROM transcripts WHERE id=$1`, existingID).Scan(&defaulted); err != nil {
		t.Fatalf("read the migrated row's session origin: %v", err)
	}
	if sessionorigin.Origin(defaulted) != sessionorigin.Unknown {
		t.Fatalf("historical row session_origin=%q, want %q so no stored transcript is demoted before the backfill runs", defaulted, sessionorigin.Unknown)
	}

	for _, origin := range sessionorigin.All {
		if _, err := pool.Exec(ctx, `UPDATE transcripts SET session_origin=$2 WHERE id=$1`, existingID, string(origin)); err != nil {
			t.Fatalf("menu member %q was rejected by the database: %v", origin, err)
		}
		var stored string
		if err := pool.QueryRow(ctx, `SELECT session_origin FROM transcripts WHERE id=$1`, existingID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != string(origin) {
			t.Fatalf("stored session_origin=%q want %q", stored, origin)
		}
	}

	for _, rejected := range []string{"subagent", "USER", "", "agent "} {
		if _, err := pool.Exec(ctx, `UPDATE transcripts SET session_origin=$2 WHERE id=$1`, existingID, rejected); err == nil {
			t.Fatalf("database accepted session_origin=%q outside the closed menu", rejected)
		}
	}

	var nulls int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcripts WHERE session_origin IS NULL`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if nulls != 0 {
		t.Fatalf("session_origin holds %d NULL rows; the column is NOT NULL", nulls)
	}
}
