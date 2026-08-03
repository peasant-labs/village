package database

import "testing"

func TestMigration029OwnerScopedAnnotationHashesShape(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/029_owner_scoped_annotation_hashes.up.sql")
	if err != nil {
		t.Fatalf("read migration 029 up: %v", err)
	}
	down, err := migrationsFS.ReadFile("migrations/029_owner_scoped_annotation_hashes.down.sql")
	if err != nil {
		t.Fatalf("read migration 029 down: %v", err)
	}
	requireMigrationSourceContains(t, string(up), "migration 029 up", "DROP CONSTRAINT IF EXISTS annotations_content_hash_key")
	requireMigrationSourceContains(t, string(up), "migration 029 up", "ADD CONSTRAINT annotations_owner_content_hash_key")
	requireMigrationSourceContains(t, string(up), "migration 029 up", "UNIQUE (owner_id, content_hash)")
	requireMigrationSourceContains(t, string(down), "migration 029 down", "DROP CONSTRAINT IF EXISTS annotations_owner_content_hash_key")
	requireMigrationSourceContains(t, string(down), "migration 029 down", "ADD CONSTRAINT annotations_content_hash_key")
	requireMigrationSourceContains(t, string(down), "migration 029 down", "UNIQUE (content_hash)")
	registered := false
	for _, migration := range migrations {
		if migration.version == 29 {
			registered = migration.file == "migrations/029_owner_scoped_annotation_hashes.up.sql"
		}
	}
	if !registered {
		t.Error("migration 029 is not registered with its owner-scoped annotation hash source")
	}
}
