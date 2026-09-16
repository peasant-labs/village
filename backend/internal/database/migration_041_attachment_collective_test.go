package database

import (
	"strings"
	"testing"
)

// TestMigration041AttachmentCollective pins the attachment's link to its
// collective without a database, and that it is registered.
//
// The shape is the point: the column is NULLABLE with ON DELETE SET NULL. A
// cascade would delete an attachment (and, through its transcripts, the
// visibility each one held before an attach widened it) when a collective is
// deleted, which would leave those transcripts shared with a collective that no
// longer exists and nothing left to restore them.
func TestMigration041AttachmentCollective(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/041_attachment_collective.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/041_attachment_collective.down.sql")
	if err != nil {
		t.Fatal(err)
	}

	upSQL := string(up)
	for _, required := range []string{
		"ALTER TABLE pull_request_attachments",
		"ADD COLUMN group_id UUID REFERENCES groups(id) ON DELETE SET NULL",
	} {
		if !strings.Contains(upSQL, required) {
			t.Fatalf("migration 041 up SQL missing %q", required)
		}
	}
	if strings.Contains(upSQL, "NOT NULL") {
		t.Fatal("migration 041 must leave group_id nullable: a deleted collective sets it null rather than cascading the attachment away")
	}
	if strings.Contains(upSQL, "ON DELETE CASCADE") {
		t.Fatal("migration 041 must not cascade: it would delete the attachment's visibility snapshots with the collective")
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS group_id") {
		t.Fatal("migration 041 down SQL does not drop group_id")
	}

	found := false
	for _, migration := range migrations {
		if migration.version == 41 {
			found = true
		}
	}
	if !found {
		t.Fatal("migration 041 is not registered")
	}
}
