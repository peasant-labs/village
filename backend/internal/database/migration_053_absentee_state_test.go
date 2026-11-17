package database

import (
	"bytes"
	_ "embed"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/migration-053-absentee-state.yaml
var absenteeStateFixtureYAML []byte

type absenteeStateCase struct {
	Name string `yaml:"name"`
}

type absenteeStateFixture struct {
	States        []string `yaml:"states"`
	InvalidStates []string `yaml:"invalid_states"`
	ShareStates   []string `yaml:"share_states"`
	Performance   struct {
		AuditRows       int `yaml:"audit_rows"`
		Transcripts     int `yaml:"transcripts"`
		Groups          int `yaml:"groups"`
		MaxMatchingRows int `yaml:"max_matching_rows"`
	} `yaml:"performance"`
	Cases []absenteeStateCase `yaml:"cases"`
}

var requiredAbsenteeStateCases = []string{
	"absentee-registers-053",
	"absentee-down-refuses",
	"content-state-defaults-present",
	"content-state-closed-set",
	"content-state-change-writes-no-governance-event",
	"retraction-audit-event-unchanged",
	"retraction-audience-captured-on-owner-delete",
	"retraction-audience-captured-on-account-cascade",
	"retraction-audience-approved-shares-only",
	"retraction-audience-only-for-shared-visibility",
	"retraction-audience-cascades-with-collective",
	"retraction-audience-append-only",
}

func loadAbsenteeStateFixtures(t *testing.T) absenteeStateFixture {
	t.Helper()
	var fixture absenteeStateFixture
	decoder := yaml.NewDecoder(bytes.NewReader(absenteeStateFixtureYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(fixture.Cases))
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("empty or duplicated absentee-state fixture %q", c.Name)
		}
		seen[c.Name] = true
	}
	for _, name := range requiredAbsenteeStateCases {
		if !seen[name] {
			t.Fatalf("missing absentee-state fixture %q", name)
		}
		delete(seen, name)
	}
	if len(seen) != 0 {
		t.Fatalf("absentee-state fixture contains unmanifested cases: %v", seen)
	}
	return fixture
}

func TestMigration053AbsenteeStateStructure(t *testing.T) {
	loadAbsenteeStateFixtures(t)
	if !isRegisteredMigration(53) {
		t.Fatal("absentee-state migration is not registered")
	}
	if _, err := migrationsFS.ReadFile("migrations/053_transcript_absentee_state.down.sql"); err != nil {
		t.Fatal(err)
	}
}
