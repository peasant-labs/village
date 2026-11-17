package database

import (
	"bytes"
	_ "embed"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/migration-052-repair-audit.yaml
var repairAuditFixtureYAML []byte

type repairAuditColumn struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Nullable bool   `yaml:"nullable"`
	Identity string `yaml:"identity"`
	Default  string `yaml:"default"`
}

type repairAuditStep struct {
	Expect     string         `yaml:"expect"`
	Fields     map[string]any `yaml:"fields"`
	Actor      string         `yaml:"actor"`
	Constraint string         `yaml:"constraint"`
	Message    string         `yaml:"message"`
	SQL        string         `yaml:"sql"`
	Affected   *int64         `yaml:"affected"`
	CheckActor bool           `yaml:"check_actor"`
}

type repairAuditCase struct {
	Name              string            `yaml:"name"`
	Kind              string            `yaml:"kind"`
	Steps             []repairAuditStep `yaml:"steps"`
	VerifyAfterCommit string            `yaml:"verify_after_commit"`
}

type repairAuditFixture struct {
	HashA   string `yaml:"hash_a"`
	HashB   string `yaml:"hash_b"`
	Catalog struct {
		Columns  []repairAuditColumn `yaml:"columns"`
		Checks   []string            `yaml:"checks"`
		Indexes  []string            `yaml:"indexes"`
		Triggers []string            `yaml:"triggers"`
	} `yaml:"catalog"`
	Defaults map[string]any    `yaml:"defaults"`
	Cases    []repairAuditCase `yaml:"cases"`
}

var requiredRepairAuditCases = []string{
	"repair-audit-registers-052",
	"repair-audit-table-shape",
	"repair-audit-phase-closed",
	"repair-audit-action-closed",
	"repair-audit-failure-class-closed",
	"repair-audit-action-identity-rules",
	"repair-audit-hash-and-size-shape",
	"repair-audit-revisions-positive",
	"repair-audit-reason-and-authorization-required",
	"repair-audit-terminal-once",
	"repair-audit-actor-derived-from-guc",
	"repair-audit-actor-required",
	"repair-audit-refuses-system-actor",
	"repair-audit-update-refused",
	"repair-audit-cascades-with-transcript",
	"repair-audit-reconciles-not-self",
	"repair-audit-refuses-direct-delete",
	"repair-audit-refused-row-keeps-prior-identity",
	"repair-audit-attempted-is-restore-only",
}

func loadRepairAuditFixtures(t *testing.T) repairAuditFixture {
	t.Helper()
	var fixture repairAuditFixture
	decoder := yaml.NewDecoder(bytes.NewReader(repairAuditFixtureYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(fixture.Cases))
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("repair audit fixture has an empty or duplicate name %q", c.Name)
		}
		if len(c.Steps) == 0 && c.Kind != "catalog" && c.Kind != "registration" {
			t.Fatalf("repair audit fixture %q has no executable assertions", c.Name)
		}
		seen[c.Name] = true
	}
	for _, name := range requiredRepairAuditCases {
		if !seen[name] {
			t.Fatalf("repair audit fixture %q is missing", name)
		}
	}
	return fixture
}

func TestMigration052ContentRepairStructure(t *testing.T) {
	loadRepairAuditFixtures(t)
	up, err := migrationsFS.ReadFile("migrations/052_transcript_content_repair.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/052_transcript_content_repair.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !isRegisteredMigration(52) || !strings.Contains(string(up), "CREATE TABLE transcript_content_repairs") {
		t.Fatal("content repair migration is not registered or has no ledger")
	}
	// The down migration removes each trigger before its function and the table.
	previous := -1
	for _, object := range []string{
		"DROP TRIGGER trg_repair_audit_delete_guard", "DROP FUNCTION repair_audit_block_delete()",
		"DROP TRIGGER trg_repair_audit_immutable", "DROP FUNCTION repair_audit_block_update()",
		"DROP TRIGGER trg_repair_audit_actor", "DROP FUNCTION repair_audit_assign_actor()",
		"DROP TABLE transcript_content_repairs",
	} {
		position := strings.Index(string(down), object)
		if position <= previous {
			t.Fatalf("content repair down migration omits or misorders %q", object)
		}
		previous = position
	}
}
