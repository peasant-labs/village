package database

import (
	"regexp"
	"strings"
	"testing"
)

// TestMigration051ContributionFamilyIndex pins the index's statement without a
// database: the table, the column order and the plain (non-concurrent) build.
//
// The column order is the point. The family expansion filters on owner_id first
// and then matches parent_session_id, so the owner must lead for the pair to be
// one index range.
func TestMigration051ContributionFamilyIndex(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/051_contribution_family_index.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/051_contribution_family_index.down.sql")
	if err != nil {
		t.Fatal(err)
	}

	// The statements, not the file: the prose above the statement names the
	// columns and the build mode, and reading the whole file would match it.
	if got, want := sqlStatementsWithoutComments(string(up)), "CREATE INDEX idx_transcripts_owner_parent_session ON transcripts (owner_id, parent_session_id);"; got != want {
		t.Fatalf("migration 051 up statements = %q, want %q", got, want)
	}
	if got, want := sqlStatementsWithoutComments(string(down)), "DROP INDEX IF EXISTS idx_transcripts_owner_parent_session;"; got != want {
		t.Fatalf("migration 051 down statements = %q, want %q", got, want)
	}
	if !isRegisteredMigration(51) {
		t.Fatal("contribution family index migration is not registered")
	}
}

var sqlLineComment = regexp.MustCompile(`(?m)^\s*--.*$`)

// sqlStatementsWithoutComments drops whole-line comments and collapses the rest
// to single spaces, so a multi-line statement compares as the statement it is.
func sqlStatementsWithoutComments(sql string) string {
	return strings.Join(strings.Fields(sqlLineComment.ReplaceAllString(sql, "")), " ")
}
