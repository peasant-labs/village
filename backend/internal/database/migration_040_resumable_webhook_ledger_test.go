package database

import (
	"strings"
	"testing"
)

// TestMigration040ResumableWebhookLedger pins the resumable ledger's shape
// without a database, and that it is registered.
//
// The columns are the point of the migration: the event type and payload so an
// attempt can be resumed from the row alone, the status and attempt count so a
// caller can tell a first attempt from a replay, and the per-status timestamps
// so an operator can see when a delivery was handled or last failed.
func TestMigration040ResumableWebhookLedger(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/040_resumable_webhook_ledger.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/040_resumable_webhook_ledger.down.sql")
	if err != nil {
		t.Fatal(err)
	}

	upSQL := string(up)
	for _, required := range []string{
		"event_type TEXT        NOT NULL DEFAULT ''",
		"payload    BYTEA       NOT NULL DEFAULT ''::bytea",
		"status     TEXT        NOT NULL DEFAULT 'pending'",
		"attempts   INTEGER     NOT NULL DEFAULT 0",
		"last_error TEXT",
		"handled_at TIMESTAMPTZ",
		"failed_at  TIMESTAMPTZ",
		"CHECK (status IN ('pending', 'handled', 'failed'))",
		"CHECK (attempts >= 0)",
		// The backfill: rows written under the old model were never retried, so
		// they are handled and must not look like a pending attempt.
		"SET status = 'handled', handled_at = received_at",
	} {
		if !strings.Contains(upSQL, required) {
			t.Fatalf("migration 040 up SQL missing %q", required)
		}
	}

	downSQL := string(down)
	for _, required := range []string{
		"DROP CONSTRAINT IF EXISTS github_webhook_deliveries_status_menu",
		"DROP CONSTRAINT IF EXISTS github_webhook_deliveries_attempts_nonnegative",
		"DROP COLUMN IF EXISTS status",
		"DROP COLUMN IF EXISTS payload",
	} {
		if !strings.Contains(downSQL, required) {
			t.Fatalf("migration 040 down SQL missing %q", required)
		}
	}

	found := false
	for _, migration := range migrations {
		if migration.version == 40 {
			found = true
		}
	}
	if !found {
		t.Fatal("migration 040 is not registered")
	}
}
