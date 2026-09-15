package database

import (
	"strings"
	"testing"
)

// TestMigration038GitHubWebhookDeliveries pins the delivery ledger's shape
// without a database, and that it is registered.
func TestMigration038GitHubWebhookDeliveries(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/038_github_webhook_deliveries.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/038_github_webhook_deliveries.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"CREATE TABLE github_webhook_deliveries",
		"delivery_id TEXT PRIMARY KEY",
		"received_at TIMESTAMPTZ NOT NULL DEFAULT now()",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("migration 038 up SQL missing %q", required)
		}
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS github_webhook_deliveries") {
		t.Fatal("migration 038 down SQL does not drop github_webhook_deliveries")
	}
	found := false
	for _, migration := range migrations {
		if migration.version == 38 {
			found = true
		}
	}
	if !found {
		t.Fatal("migration 038 is not registered")
	}
}
