//go:build integration

package database

import (
	"context"
	"testing"
)

func TestMigration050ConversationTurnCountConstraints(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	migrateTestDatabaseThrough(t, pool, 49)
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
	// A row published before the migration carries only the older turn total.
	if _, err := tx.Exec(ctx, `UPDATE transcripts SET turn_count=7 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runMigration(pool, requireMigrationVersion(t, 50)); err != nil {
		t.Fatal(err)
	}
	var unmeasured bool
	var shown *int64
	if err := pool.QueryRow(ctx, `SELECT conversation_turn_count IS NULL, displayed_turn_count FROM transcripts WHERE id=$1`, id).Scan(&unmeasured, &shown); err != nil {
		t.Fatal(err)
	}
	if !unmeasured || shown == nil || *shown != 7 {
		t.Fatalf("row published before the migration: conversation count unmeasured=%v displayed=%v, want unmeasured and the stored turn_count 7", unmeasured, shown)
	}
	for _, c := range loadConversationTurnCountFixtures(t).Cases {
		t.Run(c.Name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `UPDATE transcripts SET conversation_turn_count=$2::text::bigint, turn_count=$3 WHERE id=$1`, id, c.Count, c.TurnCount)
			if (err == nil) != c.Accepted {
				t.Fatalf("accepted=%v want %v: %v", err == nil, c.Accepted, err)
			}
			if !c.Accepted {
				return
			}
			var displayed *int64
			if err := pool.QueryRow(ctx, `SELECT displayed_turn_count FROM transcripts WHERE id=$1`, id).Scan(&displayed); err != nil {
				t.Fatal(err)
			}
			if (displayed == nil) != (c.Displayed == nil) || displayed != nil && *displayed != *c.Displayed {
				t.Fatalf("displayed_turn_count=%v want %v", derefInt64(displayed), derefInt64(c.Displayed))
			}
		})
	}
	// The displayed number has one definition; no writer may set it.
	if _, err := pool.Exec(ctx, `UPDATE transcripts SET displayed_turn_count=1 WHERE id=$1`, id); err == nil {
		t.Fatal("displayed_turn_count accepted a direct write; it must stay generated")
	}
	down, err := migrationsFS.ReadFile("migrations/050_conversation_turn_count.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("reverse conversation turn count migration: %v", err)
	}
}

func derefInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
