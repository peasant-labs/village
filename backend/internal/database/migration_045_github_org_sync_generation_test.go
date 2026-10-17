package database

import (
	"strings"
	"testing"
)

func TestMigration045GitHubOrgGenerationStructure(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/045_github_org_sync_generation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(up), "github_org_sync_generation BIGINT NOT NULL DEFAULT 0") || !strings.Contains(string(up), "CHECK (github_org_sync_generation >= 0)") {
		t.Fatal("private generation lacks required default/bound")
	}
	down, err := migrationsFS.ReadFile("migrations/045_github_org_sync_generation.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "DROP COLUMN github_org_sync_generation") {
		t.Fatal("private generation down does not remove its state")
	}
	found := false
	for _, m := range migrations {
		if m.version == 45 {
			found = true
		}
	}
	if !found {
		t.Fatal("private generation migration is not registered")
	}
}
