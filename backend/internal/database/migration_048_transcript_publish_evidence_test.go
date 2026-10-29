package database

import (
	"strings"
	"testing"
)

// TestMigration048_Structure pins what migration 048 must declare and that it
// is registered. Its behavior against PostgreSQL is proven in
// TestMigration048_Checks and TestMigration048_RoundTrip.
func TestMigration048_Structure(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/048_transcript_publish_evidence.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/048_transcript_publish_evidence.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"ADD COLUMN publish_metadata_key TEXT",
		"ADD COLUMN publish_metadata_hash TEXT",
		"ADD COLUMN publish_metadata_size BIGINT",
		"transcripts_publish_metadata_complete",
		"transcripts_publish_metadata_key_shape",
		"transcripts_publish_metadata_hash_shape",
		"transcripts_publish_metadata_size_nonnegative",
		"BEFORE INSERT OR DELETE OR UPDATE OF blob_key, wrapped_data_key, encryption_algorithm, key_version, content_hash, blob_size_bytes, publish_metadata_key, publish_metadata_hash, publish_metadata_size",
		"CREATE TABLE transcript_content_rewrites",
		"PRIMARY KEY (transcript_id, installed_content_hash)",
		"REFERENCES transcripts(id) ON DELETE CASCADE",
		"transcript_content_rewrites_replaced_hash_shape",
		"transcript_content_rewrites_installed_hash_shape",
	} {
		if !strings.Contains(string(up), required) {
			t.Errorf("migration 048 up SQL missing %q", required)
		}
	}
	for _, required := range []string{
		"DROP TABLE IF EXISTS transcript_content_rewrites",
		"BEFORE INSERT OR DELETE OR UPDATE OF blob_key, wrapped_data_key, encryption_algorithm, key_version, content_hash, blob_size_bytes\n",
		"DROP CONSTRAINT IF EXISTS transcripts_publish_metadata_size_nonnegative",
		"DROP CONSTRAINT IF EXISTS transcripts_publish_metadata_hash_shape",
		"DROP CONSTRAINT IF EXISTS transcripts_publish_metadata_key_shape",
		"DROP CONSTRAINT IF EXISTS transcripts_publish_metadata_complete",
		"DROP COLUMN IF EXISTS publish_metadata_size",
		"DROP COLUMN IF EXISTS publish_metadata_hash",
		"DROP COLUMN IF EXISTS publish_metadata_key",
	} {
		if !strings.Contains(string(down), required) {
			t.Errorf("migration 048 down SQL missing %q", required)
		}
	}
	found := false
	for _, migration := range migrations {
		if migration.version == 48 {
			found = true
		}
	}
	if !found {
		t.Fatal("migration 048 is not registered")
	}
}
