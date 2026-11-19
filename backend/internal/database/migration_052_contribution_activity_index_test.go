package database

import "testing"

// TestMigration052ContributionActivityIndex pins the index's statement without a
// database: the table, the key order and the plain (non-concurrent) build.
//
// The key is the point. The owner leads so one owner's transcripts are one
// range, then the last-activity expression the paged view orders roots by,
// descending, then id ascending as the tie-break, so the index yields the roots
// in page order and a keyset cursor resumes inside it.
func TestMigration052ContributionActivityIndex(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/052_contribution_activity_index.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/052_contribution_activity_index.down.sql")
	if err != nil {
		t.Fatal(err)
	}

	const wantUp = "CREATE INDEX idx_transcripts_owner_last_activity ON transcripts (owner_id, (COALESCE(session_end, session_start, published_at, 'epoch'::timestamptz)) DESC, id);"
	if got := sqlStatementsWithoutComments(string(up)); got != wantUp {
		t.Fatalf("migration 052 up statements = %q, want %q", got, wantUp)
	}
	if got, want := sqlStatementsWithoutComments(string(down)), "DROP INDEX IF EXISTS idx_transcripts_owner_last_activity;"; got != want {
		t.Fatalf("migration 052 down statements = %q, want %q", got, want)
	}
	if !isRegisteredMigration(52) {
		t.Fatal("contribution activity index migration is not registered")
	}
}
