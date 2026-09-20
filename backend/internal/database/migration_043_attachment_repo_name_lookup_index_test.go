package database

import (
	"strings"
	"testing"
)

// TestMigration043AttachmentRepoNameLookupIndex pins the index and its columns
// without a database.
//
// The shape is the point: the publish hook's lookup constrains author_id, the
// lower-cased repository NAME and state. The table's other index leads with
// lower(repo_owner), which that predicate never constrains — the comparison is on
// the name, because a fork's remote keeps the name and changes the owner — so an
// index leading with the owner would be the same mistake twice.
func TestMigration043AttachmentRepoNameLookupIndex(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/043_attachment_repo_name_lookup_index.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/043_attachment_repo_name_lookup_index.down.sql")
	if err != nil {
		t.Fatal(err)
	}

	// The statement, not the file: the migration's prose explains why the name is
	// compared rather than the owner, and reading the whole file would match it.
	upSQL := string(up)
	statementStart := strings.Index(upSQL, "CREATE INDEX")
	if statementStart < 0 {
		t.Fatal("migration 043 up SQL declares no CREATE INDEX")
	}
	statement := upSQL[statementStart:]
	if end := strings.Index(statement, ";"); end >= 0 {
		statement = statement[:end]
	}

	if !strings.Contains(statement, "CREATE INDEX idx_pull_request_attachments_author_repo_name_state") {
		t.Fatalf("migration 043 must create the lookup index by name; statement=%q", statement)
	}
	if !strings.Contains(statement, "ON pull_request_attachments (author_id, lower(repo_name), state)") {
		t.Fatalf("migration 043 must index author_id, lower(repo_name), state in that order, or the hook's lookup cannot use it; statement=%q", statement)
	}
	if strings.Contains(statement, "repo_owner") {
		t.Fatalf("migration 043 must not index repo_owner: the lookup does not constrain it; statement=%q", statement)
	}

	if !strings.Contains(string(down), "DROP INDEX IF EXISTS idx_pull_request_attachments_author_repo_name_state") {
		t.Fatal("migration 043 down must drop the index it created")
	}
}
