//go:build integration

package database

import (
	"context"
	"testing"
)

func TestMigration039SessionGraphConstraints(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	migrateTestDatabaseThrough(t, pool, 38)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := insertFenceOwner(t, ctx, tx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
		t.Fatal(err)
	}
	id, err := insertFenceTranscript(ctx, tx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runMigration(pool, requireMigrationVersion(t, 39)); err != nil {
		t.Fatal(err)
	}
	var absent bool
	if err := pool.QueryRow(ctx, `SELECT input_submission_count IS NULL AND root_session_id IS NULL AND session_purpose IS NULL AND session_relationships = '[]'::jsonb FROM transcripts WHERE id=$1`, id).Scan(&absent); err != nil {
		t.Fatal(err)
	}
	if !absent {
		t.Fatal("historical graph/count absence was backfilled")
	}
	for _, c := range loadGraphProjectionFixtures(t).Cases {
		t.Run(c.Name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `UPDATE transcripts SET input_submission_count=$2::text::bigint, session_purpose=$3, root_session_id=$4, session_relationships=$5::jsonb WHERE id=$1`, id, c.Count, c.Purpose, c.Root, c.Relationships)
			if (err == nil) != c.Accepted {
				t.Fatalf("accepted=%v want %v: %v", err == nil, c.Accepted, err)
			}
		})
	}
	down, err := migrationsFS.ReadFile("migrations/039_session_graph_provenance.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("reverse graph migration: %v", err)
	}
}
