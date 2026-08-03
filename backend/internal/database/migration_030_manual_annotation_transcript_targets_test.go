package database

import (
	"strings"
	"testing"
)

func TestMigration030ManualAnnotationTranscriptTargetsShape(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/030_manual_annotation_transcript_targets.up.sql")
	if err != nil {
		t.Fatalf("read migration 030 up: %v", err)
	}
	down, err := migrationsFS.ReadFile("migrations/030_manual_annotation_transcript_targets.down.sql")
	if err != nil {
		t.Fatalf("read migration 030 down: %v", err)
	}

	requireMigrationSourceContains(t, string(up), "migration 030 up", "ADD COLUMN IF NOT EXISTS target_transcript_id UUID")
	requireMigrationSourceContains(t, string(up), "migration 030 up", "annotations_target_transcript_id_fk")
	requireMigrationSourceContains(t, string(up), "migration 030 up", "REFERENCES transcripts (id)")
	requireMigrationSourceContains(t, string(up), "migration 030 up", "ON DELETE CASCADE")
	requireMigrationSourceContains(t, string(up), "migration 030 up", "idx_annotations_target_transcript")
	requireMigrationSourceContains(t, string(up), "migration 030 up", "a.annotator_kind = 'human'")
	requireMigrationSourceContains(t, string(up), "migration 030 up", "HAVING COUNT(DISTINCT t.id) = 1")
	if strings.Contains(string(up), "t.owner_id = a.owner_id") {
		t.Error("migration 030 up still restricts legacy backfill to annotation owners")
	}
	requireMigrationSourceContains(t, string(down), "migration 030 down", "DROP CONSTRAINT IF EXISTS annotations_target_transcript_id_fk")
	requireMigrationSourceContains(t, string(down), "migration 030 down", "DROP COLUMN IF EXISTS target_transcript_id")

	registered := false
	for _, migration := range migrations {
		if migration.version == 30 {
			registered = migration.file == "migrations/030_manual_annotation_transcript_targets.up.sql"
		}
	}
	if !registered {
		t.Error("migration 030 is not registered with its manual annotation target source")
	}
}
