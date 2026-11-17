//go:build integration

package database

import (
	"context"
	"testing"
)

func TestMigration051ContentRevisionRealPostgres(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	migrateTestDatabaseThrough(t, pool, 50)
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
	if _, err := tx.Exec(ctx, "UPDATE transcripts SET content_hash='legacy-malformed', blob_size_bytes=NULL WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var before uint32
	if err := pool.QueryRow(ctx, "SELECT relfilenode FROM pg_class WHERE oid='transcripts'::regclass").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := runMigration(pool, requireMigrationVersion(t, 51)); err != nil {
		t.Fatal(err)
	}
	for _, c := range loadMigration051Cases(t) {
		t.Run(c.Name, func(t *testing.T) {
			switch c.Name {
			case "migration-null-and-malformed-tolerated":
				var revision int64
				var hash string
				var alternate *string
				if err := pool.QueryRow(ctx, "SELECT content_revision, content_hash, segmented_identity_hash FROM transcripts WHERE id=$1", id).Scan(&revision, &hash, &alternate); err != nil {
					t.Fatal(err)
				}
				if revision != 1 || hash != "legacy-malformed" || alternate != nil {
					t.Fatalf("historical row changed: revision=%d hash=%q alternate=%v", revision, hash, alternate)
				}
				return
			case "migration-constant-default-metadata-only":
				var after uint32
				var missing bool
				if err := pool.QueryRow(ctx, "SELECT relfilenode FROM pg_class WHERE oid='transcripts'::regclass").Scan(&after); err != nil {
					t.Fatal(err)
				}
				if err := pool.QueryRow(ctx, "SELECT atthasmissing FROM pg_attribute WHERE attrelid='transcripts'::regclass AND attname='content_revision'").Scan(&missing); err != nil {
					t.Fatal(err)
				}
				if before != after || !missing {
					t.Fatalf("migration rewrote the table or missed the fast default: before=%d after=%d fast_default=%v", before, after, missing)
				}
				return
			}
			if c.Assignment == "" {
				t.Fatal("constraint case has no assignment")
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true)"); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, "UPDATE transcripts SET "+c.Assignment+" WHERE id=$1", id)
			if (err == nil) != c.Accepted {
				t.Fatalf("accepted=%v want %v: %v", err == nil, c.Accepted, err)
			}
		})
	}
	down, err := migrationsFS.ReadFile("migrations/051_transcript_content_revision.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("forward-only migration accepted a downgrade")
	}
}
