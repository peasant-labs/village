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

type repairAuditExpectation string
type repairAuditActor string
type repairAuditCaseKind string

const (
	repairAuditAccept repairAuditExpectation = "accept"
	repairAuditReject repairAuditExpectation = "reject"
	repairAuditTrue   repairAuditExpectation = "true"

	repairAuditActorMissing repairAuditActor = "missing"
	repairAuditActorSystem  repairAuditActor = "system"

	repairAuditRegistration repairAuditCaseKind = "registration"
	repairAuditCatalog      repairAuditCaseKind = "catalog"
	repairAuditDownRefusal  repairAuditCaseKind = "down_refusal"
)

type repairAuditStep struct {
	Expect     repairAuditExpectation `yaml:"expect"`
	Fields     map[string]any         `yaml:"fields"`
	Actor      repairAuditActor       `yaml:"actor"`
	Constraint string                 `yaml:"constraint"`
	Message    string                 `yaml:"message"`
	SQL        string                 `yaml:"sql"`
	Affected   *int64                 `yaml:"affected"`
	CheckActor bool                   `yaml:"check_actor"`
}

type repairAuditCase struct {
	Name              string              `yaml:"name"`
	Kind              repairAuditCaseKind `yaml:"kind"`
	Steps             []repairAuditStep   `yaml:"steps"`
	VerifyAfterCommit string              `yaml:"verify_after_commit"`
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
	"repair-audit-declare-unavailable-shape",
	"repair-audit-down-refuses",
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
		if len(c.Steps) == 0 && c.Kind != repairAuditCatalog && c.Kind != repairAuditRegistration && c.Kind != repairAuditDownRefusal {
			t.Fatalf("repair audit fixture %q has no executable assertions", c.Name)
		}
		if c.Kind == repairAuditDownRefusal && len(c.Steps) == 0 {
			t.Fatalf("repair audit fixture %q must seed a row before the down file runs", c.Name)
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
	if !isRegisteredMigration(52) || !strings.Contains(string(up), "CREATE TABLE transcript_content_repairs") ||
		!strings.Contains(string(up), "'declare_unavailable'") ||
		!strings.Contains(string(down), "forward-only") || !strings.Contains(string(down), "RAISE EXCEPTION") {
		t.Fatal("content repair migration registration, declare action or forward-only refusal is missing")
	}
	if strings.Contains(strings.ToUpper(string(down)), "DROP TABLE") {
		t.Fatal("content repair down migration must refuse instead of dropping the ledger")
	}
}
