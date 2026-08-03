package database

import (
	"strings"
	"testing"
)

func TestMigration031RegisteredAndTransactionOwned(t *testing.T) {
	var registered bool
	for _, m := range migrations {
		if m.version == 31 && m.file == "migrations/031_transcript_encryption.up.sql" {
			registered = true
		}
	}
	if !registered {
		t.Fatal("migration 031 is not registered with its encryption migration file")
	}
	up, err := migrationsFS.ReadFile("migrations/031_transcript_encryption.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToUpper(string(up))
	assertSQLExcludes(t, sql, "BEGIN;")
	assertSQLExcludes(t, sql, "COMMIT;")
	assertSQLExcludes(t, sql, "CREATE INDEX CONCURRENTLY")
	assertSQLExcludes(t, sql, "VACUUM ")
	assertSQLContains(t, sql, "ACCESS EXCLUSIVE")
	assertSQLContains(t, sql, "WRAPPED_DATA_KEY BYTEA NOT NULL")
	assertSQLContains(t, sql, "APP.TRANSCRIPT_WRITER_VERSION")
	assertSQLContains(t, sql, "TRG_TRANSCRIPT_WRITER_VERSION")
}

func assertSQLExcludes(t *testing.T, sql, token string) {
	t.Helper()
	if strings.Contains(sql, token) {
		t.Fatalf("migration 031 contains transaction-incompatible token %q", token)
	}
}

func assertSQLContains(t *testing.T, sql, token string) {
	t.Helper()
	if !strings.Contains(sql, token) {
		t.Errorf("migration 031 missing required production DDL %q", token)
	}
}
