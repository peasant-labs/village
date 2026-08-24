package database

import (
	"fmt"
	"strings"
	"testing"

	"github.com/peasant-labs/village/backend/internal/sessionorigin"
)

func TestMigration033SessionOrigin(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/033_session_origin.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/033_session_origin.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"session_origin TEXT NOT NULL DEFAULT 'unknown'",
		"transcripts_session_origin_menu",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("migration 033 up SQL missing %q", required)
		}
	}
	// The accepted set is derived from the Go menu, never restated here, so
	// widening sessionorigin.All without widening the CHECK fails this test.
	for _, origin := range sessionorigin.All {
		if !strings.Contains(string(up), fmt.Sprintf("'%s'", origin)) {
			t.Fatalf("migration 033 CHECK omits menu member %q", origin)
		}
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS session_origin") {
		t.Fatal("migration 033 down SQL does not remove the session_origin column")
	}
	if !strings.Contains(string(down), "DROP CONSTRAINT IF EXISTS transcripts_session_origin_menu") {
		t.Fatal("migration 033 down SQL does not remove the session_origin menu check")
	}
	found := false
	for _, migration := range migrations {
		if migration.version == 33 {
			found = true
		}
	}
	if !found {
		t.Fatal("migration 033 is not registered")
	}
}
