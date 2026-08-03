package database

import (
	"strings"
	"testing"
)

func TestMigration032AuthoritativePublicationReceipts(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/032_authoritative_publication_receipts.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/032_authoritative_publication_receipts.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"accepted_request_operation_fingerprint TEXT", "^[0-9a-f]{64}$", "transcripts_accepted_request_operation_fingerprint_shape"} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("migration 032 up SQL missing %q", required)
		}
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS accepted_request_operation_fingerprint") {
		t.Fatal("migration 032 down SQL does not remove the fingerprint column")
	}
	found := false
	for _, migration := range migrations {
		if migration.version == 32 {
			found = true
		}
	}
	if !found {
		t.Fatal("migration 032 is not registered")
	}
}
