package database

import (
	"strings"
	"testing"
)

// TestMigration044AttachWidenedAndAutoAttach pins the two columns without a
// database, and that the migration is registered.
//
// The order of the two binding statements is the point. The constant default
// marks every binding that exists when the migration runs, and each of those was
// written by an attach that widened its transcript. Dropping the default right
// after means no later insert can inherit true by omission: a new binding that
// detach would narrow must be stated, never defaulted.
func TestMigration044AttachWidenedAndAutoAttach(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/044_attach_widened_and_auto_attach.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/044_attach_widened_and_auto_attach.down.sql")
	if err != nil {
		t.Fatal(err)
	}

	upSQL := string(up)
	addWidened := "ADD COLUMN attach_widened BOOLEAN NOT NULL DEFAULT true"
	dropDefault := "ALTER COLUMN attach_widened DROP DEFAULT"
	for _, required := range []string{
		"ALTER TABLE pull_request_attachment_transcripts",
		addWidened,
		dropDefault,
		"ALTER TABLE users",
		"ADD COLUMN auto_attach_pull_requests BOOLEAN NOT NULL DEFAULT false",
	} {
		if !strings.Contains(upSQL, required) {
			t.Fatalf("migration 044 up SQL missing %q", required)
		}
	}
	if strings.Index(upSQL, dropDefault) < strings.Index(upSQL, addWidened) {
		t.Fatal("migration 044 must add attach_widened with its default before dropping the default, so existing bindings are marked and new ones are not")
	}

	downSQL := string(down)
	for _, required := range []string{
		"DROP COLUMN IF EXISTS attach_widened",
		"DROP COLUMN IF EXISTS auto_attach_pull_requests",
	} {
		if !strings.Contains(downSQL, required) {
			t.Fatalf("migration 044 down SQL missing %q", required)
		}
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
