package database

// Migration 024 adds transcripts.content_hash for pull currency checks.
//
// This file has TWO tests:
//   - TestMigration024_Registered / TestMigration024_SQL run in the default
//     `go test ./...` (no DB): they assert the migration is registered in the
//     ordered list and that the embedded up/down SQL adds/drops the column.
//   - TestMigration024_AppliesContentHashColumn is build-tagged `integration`
//     (see migration_024_content_hash_integration_test.go) and applies the real
//     migration against a live Postgres.

import (
	"strings"
	"testing"
)

func TestMigration024_Registered(t *testing.T) {
	var found bool
	for _, m := range migrations {
		if m.version == 24 {
			found = true
			if m.file != "migrations/024_transcript_content_hash.up.sql" {
				t.Fatalf("migration 24 file = %q, want migrations/024_transcript_content_hash.up.sql", m.file)
			}
		}
	}
	if !found {
		t.Fatal("migration version 24 is not registered in the migrations list")
	}
}

func TestMigration024_SQL(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/024_transcript_content_hash.up.sql")
	if err != nil {
		t.Fatalf("read 024 up migration: %v", err)
	}
	upSQL := strings.ToLower(string(up))
	if !strings.Contains(upSQL, "alter table transcripts") || !strings.Contains(upSQL, "add column content_hash") {
		t.Fatalf("024 up migration must ADD COLUMN content_hash on transcripts; got:\n%s", up)
	}
	// Must be nullable: legacy rows have no hash until the backfill runs, and a
	// NULL hash is the documented "no ETag => unconditional GET" fallback.
	if strings.Contains(upSQL, "not null") {
		t.Fatalf("024 content_hash must be NULLABLE (NULL => no-ETag fallback); got:\n%s", up)
	}

	down, err := migrationsFS.ReadFile("migrations/024_transcript_content_hash.down.sql")
	if err != nil {
		t.Fatalf("read 024 down migration: %v", err)
	}
	downSQL := strings.ToLower(string(down))
	if !strings.Contains(downSQL, "drop column") || !strings.Contains(downSQL, "content_hash") {
		t.Fatalf("024 down migration must DROP COLUMN content_hash; got:\n%s", down)
	}
}
