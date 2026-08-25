package database

import (
	"strings"
	"testing"
)

// The reserved menus are asserted as exact CHECK text because widening either
// one is a schema decision, not an implementation detail: a new target_kind or
// field must arrive with its own migration and its own review.
func TestMigration034OwnerOverrides(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/034_owner_overrides.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/034_owner_overrides.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"CREATE TABLE owner_overrides",
		"REFERENCES users(id) ON DELETE CASCADE",
		"CHECK (target_kind IN ('project', 'transcript', 'redaction_span'))",
		"CHECK (field IN ('display_name', 'title', 'redaction_decision'))",
		"CHECK (char_length(value) BETWEEN 1 AND 4096)",
		"PRIMARY KEY (owner_id, target_kind, target_key, field)",
		"CREATE INDEX idx_owner_overrides_owner_kind",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("owner_overrides migration up SQL missing %q", required)
		}
	}
	// The no-audit decision is deliberate and reversible only on a stated
	// condition. If a later change attaches an audit trigger to this table, the
	// decision recorded in docs/database-invariants.md has to be revisited in
	// the same commit, so the two must not drift apart silently.
	executable := sqlWithoutLineComments(string(up))
	if strings.Contains(executable, "app.actor_id") || strings.Contains(executable, "CREATE TRIGGER") {
		t.Fatal("owner_overrides carries no governance audit trigger and requires no actor GUC; if that changed, update the recorded decision and its reversing condition in docs/database-invariants.md in the same commit")
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS owner_overrides") {
		t.Fatal("owner_overrides down SQL does not remove the table")
	}
	if !isRegisteredMigration(34) {
		t.Fatal("owner_overrides migration is not registered")
	}
}

// sqlWithoutLineComments drops -- comment lines so a guard reads what the
// database executes, not what the file explains.
func sqlWithoutLineComments(sql string) string {
	var kept []string
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func isRegisteredMigration(version int) bool {
	for _, migration := range migrations {
		if migration.version == version {
			return true
		}
	}
	return false
}
