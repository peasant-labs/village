package database

import "testing"

// wantLatestMigration is bumped in the SAME commit that adds a migration.
//
// This file is the ONE home of the registry-wide invariants (strictly-increasing
// versions + highest == newest). It exists so the invariant never again roves
// from migration_NNN_test.go to migration_NNN+1_test.go — that pattern forced
// editing a PRIOR migration's test on every new migration, violating the
// "do not retrofit prior migration tests" rule (AGENTS.md), and the 024→025 move
// dropped an assertion in transit. Per-migration test files assert only their own
// migration's registration and shape.
const wantLatestMigration = 33

func TestMigrationsRegistry(t *testing.T) {
	prev := 0
	for _, m := range migrations {
		if m.version <= prev {
			t.Fatalf("migrations not strictly increasing: %d after %d", m.version, prev)
		}
		prev = m.version
	}
	if prev != wantLatestMigration {
		t.Fatalf("highest registered migration = %d, want %d — if you added a migration, bump wantLatestMigration in the same commit; if not, a version is misnumbered", prev, wantLatestMigration)
	}
}
