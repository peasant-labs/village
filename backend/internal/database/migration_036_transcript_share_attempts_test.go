package database

import (
	"strings"
	"testing"
)

// The literals asserted here are the ones whose silent loss would leave every
// other test green: a trigger narrowed to INSERT still propagates submissions,
// a fail-closed trigger scoped to one verb still blocks that verb, and a
// terminal status written into the derived row still reads back as a share.
func TestMigration036ShareAttemptModel(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/036_transcript_share_attempts.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/036_transcript_share_attempts.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"CREATE TABLE transcript_share_attempts",
		"CHECK (status IN ('pending', 'approved', 'rejected',",
		"'retracted', 'revoked'))",
		"UNIQUE (transcript_id, group_id, attempt_no)",
		"CREATE UNIQUE INDEX uq_share_attempt_open",
		"WHERE status = 'pending'",
		"INSERT INTO transcript_share_attempts (transcript_id, group_id, attempt_no, status, submitted_at)",
		// Decisions, retractions and revocations are UPDATEs of an existing
		// attempt, so an INSERT-only derivation stops propagating all three.
		"AFTER INSERT OR UPDATE ON transcript_share_attempts",
		// All three verbs, separately named: a guard scoped to one of them
		// leaves the other two open.
		"BEFORE INSERT OR UPDATE OR DELETE ON transcript_shares",
		"BEFORE UPDATE ON transcript_share_attempts",
		"app.share_state_derivation",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("share-attempt migration up SQL missing %q", required)
		}
	}
	// transcript_shares.status keeps its shipped three-value CHECK, which this
	// migration does not alter. Writing either terminal status into the derived
	// row would violate that CHECK on the first real database run.
	derived := string(up)[strings.Index(string(up), "INSERT INTO transcript_shares"):]
	for _, forbidden := range []string{"'retracted'", "'revoked'"} {
		if strings.Contains(strings.SplitN(derived, "ELSE", 2)[0], forbidden) {
			t.Fatalf("the derived current-state row must never carry %s; the row is deleted for that state instead", forbidden)
		}
	}
	if strings.Contains(string(up), "ALTER TABLE transcript_shares") {
		t.Fatal("this migration must not alter transcript_shares; its three-value status CHECK is deliberately unchanged")
	}
	for _, required := range []string{
		"DROP TRIGGER IF EXISTS trg_share_attempt_immutable",
		"DROP TRIGGER IF EXISTS trg_transcript_shares_fail_closed",
		"DROP TRIGGER IF EXISTS trg_derive_transcript_share",
		"DROP TABLE IF EXISTS transcript_share_attempts",
	} {
		if !strings.Contains(string(down), required) {
			t.Fatalf("share-attempt down SQL missing %q", required)
		}
	}
	if !isRegisteredMigration(36) {
		t.Fatal("share-attempt migration is not registered")
	}
}
