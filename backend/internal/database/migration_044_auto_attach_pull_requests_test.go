package database

import (
	"strings"
	"testing"
)

// TestMigration044AutoAttachPullRequests pins the column without a database, and
// that the migration is registered.
func TestMigration044AutoAttachPullRequests(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/044_auto_attach_pull_requests.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/044_auto_attach_pull_requests.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if required := "ADD COLUMN auto_attach_pull_requests BOOLEAN NOT NULL DEFAULT false"; !strings.Contains(string(up), required) {
		t.Fatalf("migration 044 up SQL missing %q", required)
	}
	if required := "DROP COLUMN IF EXISTS auto_attach_pull_requests"; !strings.Contains(string(down), required) {
		t.Fatalf("migration 044 down SQL missing %q", required)
	}
	found := false
	for _, migration := range migrations {
		if migration.version == 44 {
			found = true
		}
	}
	if !found {
		t.Fatal("migration 044 is not registered")
	}
}
