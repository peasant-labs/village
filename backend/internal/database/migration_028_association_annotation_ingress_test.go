package database

import (
	"strings"
	"testing"
)

// TestMigration028AssociationAnnotationIngressShape keeps the cheap migration
// contract in the unit suite. Live FK, uniqueness, and immutability behaviour is
// exercised by the integration family because it requires PostgreSQL.
func TestMigration028AssociationAnnotationIngressShape(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/028_association_annotation_ingress.up.sql")
	if err != nil {
		t.Fatalf("read migration 028 up: %v", err)
	}
	down, err := migrationsFS.ReadFile("migrations/028_association_annotation_ingress.down.sql")
	if err != nil {
		t.Fatalf("read migration 028 down: %v", err)
	}

	requireMigrationSourceContains(t, string(up), "migration 028 up", "CREATE TABLE transcript_associations")
	requireMigrationSourceContains(t, string(up), "migration 028 up", "PRIMARY KEY (owner_id, association_id)")
	requireMigrationSourceContains(t, string(up), "migration 028 up", "UNIQUE (owner_id, transcript_id, observed_commit_sha)")
	requireMigrationSourceContains(t, string(up), "migration 028 up", "REFERENCES transcripts (owner_id, id)")
	requireMigrationSourceContains(t, string(up), "migration 028 up", "transcript_associations_id_shape")
	requireMigrationSourceContains(t, string(up), "migration 028 up", "trg_transcript_associations_immutable")
	requireMigrationSourceContains(t, string(up), "migration 028 up", "ADD COLUMN target_association_id")
	requireMigrationSourceContains(t, string(up), "migration 028 up", "annotations_target_association_exclusive")
	requireMigrationSourceContains(t, string(up), "migration 028 up", "annotations_target_association_owner_fk")
	requireMigrationSourceContains(t, string(up), "migration 028 up", "idx_annotations_target_association")
	requireMigrationSourceContains(t, string(down), "migration 028 down", "DROP TABLE IF EXISTS transcript_associations")
	requireMigrationSourceContains(t, string(down), "migration 028 down", "DROP COLUMN IF EXISTS target_association_id")
	requireMigrationSourceContains(t, string(down), "migration 028 down", "DROP CONSTRAINT IF EXISTS transcripts_owner_id_id_key")

	registered := false
	for _, migration := range migrations {
		if migration.version == 28 {
			registered = migration.file == "migrations/028_association_annotation_ingress.up.sql"
		}
	}
	if !registered {
		t.Error("migration 028 is not registered with its association ingress source")
	}
}

func requireMigrationSourceContains(t *testing.T, source, sourceName, fragment string) {
	t.Helper()
	if !strings.Contains(source, fragment) {
		t.Errorf("%s is missing %q", sourceName, fragment)
	}
}
