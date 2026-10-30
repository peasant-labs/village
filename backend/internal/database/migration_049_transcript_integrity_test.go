package database

import (
	"strings"
	"testing"
)

// TestMigration049_Structure pins what migration 049 must declare and that it
// is registered. Its behavior against PostgreSQL is proven in
// TestMigration049_Checks and TestMigration049_RoundTrip.
func TestMigration049_Structure(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/049_transcript_integrity.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/049_transcript_integrity.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"CREATE TABLE transcript_integrity_results",
		"CREATE TABLE transcript_integrity_inherited_flags",
		"CREATE TABLE integrity_flag_tombstones",
		"CREATE TABLE transcript_integrity_attempts",
		"CREATE VIEW transcript_integrity_open_flags",
		"string_to_array(v.scry_version, '.')::numeric[] > string_to_array(r.scry_version, '.')::numeric[]",
		"PRIMARY KEY (transcript_id, content_hash, scry_version)",
		"PRIMARY KEY (owner_id, local_id)",
		"REFERENCES transcripts(id) ON DELETE CASCADE",
		"REFERENCES users(id) ON DELETE CASCADE",
		"transcript_integrity_results_hash_shape",
		"transcript_integrity_results_version_shape",
		"transcript_integrity_results_verdict_closed",
		"transcript_integrity_results_unchecked_has_no_turns",
		"integrity_flag_tombstones_local_id_nonempty",
		"transcript_integrity_attempts_hash_shape",
		"transcript_integrity_attempts_version_shape",
		"transcript_integrity_attempts_status_closed",
		"transcript_integrity_attempts_attempts_nonnegative",
		"transcript_integrity_attempts_last_error_closed",
		"'read_failed'",
		"'legacy_content'",
	} {
		if !strings.Contains(string(up), required) {
			t.Errorf("migration 049 up SQL missing %q", required)
		}
	}
	for _, forbidden := range []string{"user_daily_quotas", "trigger", "TRIGGER", "no_hash"} {
		if strings.Contains(string(up), forbidden) {
			t.Errorf("migration 049 up SQL must not contain %q", forbidden)
		}
	}
	for _, required := range []string{
		"DROP VIEW IF EXISTS transcript_integrity_open_flags",
		"DROP TABLE IF EXISTS transcript_integrity_attempts",
		"DROP TABLE IF EXISTS integrity_flag_tombstones",
		"DROP TABLE IF EXISTS transcript_integrity_inherited_flags",
		"DROP TABLE IF EXISTS transcript_integrity_results",
	} {
		if !strings.Contains(string(down), required) {
			t.Errorf("migration 049 down SQL missing %q", required)
		}
	}
	found := false
	for _, migration := range migrations {
		if migration.version == 49 {
			found = true
		}
	}
	if !found {
		t.Fatal("migration 049 is not registered")
	}
}
