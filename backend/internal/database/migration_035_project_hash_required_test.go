package database

import (
	"strings"
	"testing"
)

func TestMigration035ProjectHashRequired(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/035_project_hash_required.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/035_project_hash_required.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(up), "ALTER TABLE transcripts ALTER COLUMN project_hash SET NOT NULL") {
		t.Fatal("project_hash migration does not make the column required")
	}
	// A backfill would mask exactly the condition this migration must surface:
	// if a null hash exists, the constraint has to fail loudly rather than
	// invent an identity for a transcript that never reported one.
	for _, forbidden := range []string{"UPDATE transcripts", "DELETE FROM transcripts", "COALESCE"} {
		if strings.Contains(string(up), forbidden) {
			t.Fatalf("project_hash migration must not rewrite or remove rows, found %q", forbidden)
		}
	}
	if !strings.Contains(string(down), "DROP NOT NULL") {
		t.Fatal("project_hash down SQL does not restore the nullable column")
	}
	if !isRegisteredMigration(35) {
		t.Fatal("project_hash migration is not registered")
	}
}
