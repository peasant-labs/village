package database

import (
	"strings"
	"testing"
)

// TestMigration046AutoAttachDefault pins the new default and that the migration
// is registered.
func TestMigration046AutoAttachDefault(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/046_auto_attach_pull_requests_default.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/046_auto_attach_pull_requests_default.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if required := "ALTER COLUMN auto_attach_pull_requests SET DEFAULT true"; !strings.Contains(string(up), required) {
		t.Fatalf("migration 046 up SQL missing %q", required)
	}
	if required := "ALTER COLUMN auto_attach_pull_requests SET DEFAULT false"; !strings.Contains(string(down), required) {
		t.Fatalf("migration 046 down SQL missing %q", required)
	}
	found := false
	for _, migration := range migrations {
		if migration.version == 46 {
			found = true
		}
	}
	if !found {
		t.Fatal("migration 046 is not registered")
	}
}
