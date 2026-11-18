package database

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/migration-054-revision-guard.yaml
var revisionGuardFixtureYAML []byte

// guardOutcome is what a step must do to the database.
type guardOutcome string

const (
	guardCommit    guardOutcome = "commit"
	guardRefused   guardOutcome = "refused"
	guardUnmatched guardOutcome = "unmatched"
)

// guardWriter names a statement the fixture runs through its production
// implementation. The empty value is a raw `sql` step.
type guardWriter string

const (
	guardWriterRaw     guardWriter = ""
	guardWriterLegacy  guardWriter = "legacy"
	guardWriterInstall guardWriter = "install"
	guardWriterReplace guardWriter = "replace"
	guardWriterRepair  guardWriter = "repair"
)

var allGuardWriters = []guardWriter{guardWriterRaw, guardWriterLegacy, guardWriterInstall, guardWriterReplace, guardWriterRepair}

var allGuardOutcomes = []guardOutcome{guardCommit, guardRefused, guardUnmatched}

type revisionGuardSeed struct {
	Hash          *string `yaml:"hash"`
	Size          *string `yaml:"size"`
	SegmentedHash *string `yaml:"segmented_hash"`
	SegmentedSize *string `yaml:"segmented_size"`
	Revision      *int64  `yaml:"revision"`
}

type revisionGuardStep struct {
	Writer   guardWriter  `yaml:"writer"`
	SQL      string       `yaml:"sql"`
	Hash     string       `yaml:"hash"`
	Size     *int64       `yaml:"size"`
	Supplies int64        `yaml:"supplies"`
	Expect   guardOutcome `yaml:"expect"`
	Rule     string       `yaml:"rule"`
}

type revisionGuardFinal struct {
	Revision int64  `yaml:"revision"`
	Row      string `yaml:"row"`
}

type revisionGuardConcurrent struct {
	First  revisionGuardStep `yaml:"first"`
	Second revisionGuardStep `yaml:"second"`
}

type revisionGuardTrial struct {
	Name              string                   `yaml:"name"`
	Seed              revisionGuardSeed        `yaml:"seed"`
	SingleTransaction bool                     `yaml:"single_transaction"`
	Steps             []revisionGuardStep      `yaml:"steps"`
	Concurrent        *revisionGuardConcurrent `yaml:"concurrent"`
	Final             revisionGuardFinal       `yaml:"final"`
}

type revisionGuardCase struct {
	Name   string               `yaml:"name"`
	Stage  string               `yaml:"stage"`
	Trials []revisionGuardTrial `yaml:"trials"`
}

type revisionGuardFixture struct {
	LegacyReplaceSQL    string              `yaml:"legacy_replace_sql"`
	FirstReadInstallSQL string              `yaml:"first_read_install_sql"`
	Cases               []revisionGuardCase `yaml:"cases"`
}

// requiredRevisionGuardCases is the exact set of cases the fixture holds: an
// unlisted case and a deleted case both fail.
var requiredRevisionGuardCases = []string{
	"old-binary-pending-replacement-refused",
	"old-binary-known-replacement-counts",
	"current-writer-pending-replacement-commits-under-guard",
	"install-a-hash-only-completes-same-revision",
	"install-b-same-descriptor-keeps",
	"install-b-new-descriptor-refused",
	"b-move-without-a-refused",
	"a-cleared-b-install-refused",
	"b-b-different-refused",
	"usable-hash-clear-refused",
	"same-object-hash-change-refused",
	"concurrent-first-read-wins",
	"publish-wins-before-first-read",
	"changed-descriptor-invalidates-preread",
}

func loadRevisionGuardFixtures(t *testing.T) revisionGuardFixture {
	t.Helper()
	var fixture revisionGuardFixture
	decoder := yaml.NewDecoder(bytes.NewReader(revisionGuardFixtureYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.LegacyReplaceSQL == "" || fixture.FirstReadInstallSQL == "" {
		t.Fatal("revision guard fixture must carry its legacy replacement and first-read install statements")
	}
	seen := make(map[string]bool, len(fixture.Cases))
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("empty or duplicated revision guard fixture %q", c.Name)
		}
		seen[c.Name] = true
		if c.Stage != "n1" {
			t.Fatalf("revision guard fixture %q has stage %q, want n1", c.Name, c.Stage)
		}
		if len(c.Trials) == 0 {
			t.Fatalf("revision guard fixture %q has no trial", c.Name)
		}
		trials := make(map[string]bool, len(c.Trials))
		for _, trial := range c.Trials {
			if err := validateRevisionGuardTrial(trial); err != nil {
				t.Fatalf("revision guard fixture %q: %v", c.Name, err)
			}
			if trials[trial.Name] {
				t.Fatalf("revision guard fixture %q repeats trial %q", c.Name, trial.Name)
			}
			trials[trial.Name] = true
		}
	}
	for _, name := range requiredRevisionGuardCases {
		if !seen[name] {
			t.Fatalf("missing revision guard fixture %q", name)
		}
		delete(seen, name)
	}
	if len(seen) != 0 {
		t.Fatalf("revision guard fixture contains unmanifested cases: %v", seen)
	}
	return fixture
}

func validateRevisionGuardTrial(trial revisionGuardTrial) error {
	if trial.Name == "" {
		return fmt.Errorf("a trial has no name")
	}
	steps := append([]revisionGuardStep(nil), trial.Steps...)
	if trial.Concurrent != nil {
		if len(trial.Steps) != 0 {
			return fmt.Errorf("trial %q mixes steps with a concurrent pair", trial.Name)
		}
		if trial.Concurrent.First.Expect != guardCommit {
			return fmt.Errorf("trial %q: the first of a concurrent pair must commit", trial.Name)
		}
		steps = []revisionGuardStep{trial.Concurrent.First, trial.Concurrent.Second}
	}
	if len(steps) == 0 {
		return fmt.Errorf("trial %q has no step", trial.Name)
	}
	if trial.SingleTransaction {
		if trial.Concurrent != nil {
			return fmt.Errorf("trial %q cannot be both a single transaction and a concurrent pair", trial.Name)
		}
		for i, step := range trial.Steps {
			if step.Expect == guardRefused && i != len(trial.Steps)-1 {
				return fmt.Errorf("trial %q: a refusal ends its transaction, so only the last step may be refused", trial.Name)
			}
		}
	}
	if trial.Final.Revision < 1 {
		return fmt.Errorf("trial %q names no final revision", trial.Name)
	}
	for _, step := range steps {
		if !containsGuardWriter(step.Writer) || !containsGuardOutcome(step.Expect) {
			return fmt.Errorf("trial %q has a step with writer %q or outcome %q outside the closed sets", trial.Name, step.Writer, step.Expect)
		}
		if (step.Writer == guardWriterRaw) != (step.SQL != "") {
			return fmt.Errorf("trial %q: a step is either a raw sql step or a named writer, never both or neither", trial.Name)
		}
		if (step.Expect == guardRefused) != (step.Rule != "") {
			return fmt.Errorf("trial %q: a refused step names its rule and no other step does", trial.Name)
		}
		if (step.Writer == guardWriterReplace || step.Writer == guardWriterRepair) && step.Supplies < 1 {
			return fmt.Errorf("trial %q: a %s step supplies the revision it writes", trial.Name, step.Writer)
		}
	}
	return nil
}

func containsGuardWriter(writer guardWriter) bool {
	for _, candidate := range allGuardWriters {
		if candidate == writer {
			return true
		}
	}
	return false
}

func containsGuardOutcome(outcome guardOutcome) bool {
	for _, candidate := range allGuardOutcomes {
		if candidate == outcome {
			return true
		}
	}
	return false
}

func TestMigration054RevisionGuardStructure(t *testing.T) {
	loadRevisionGuardFixtures(t)
	if !isRegisteredMigration(54) {
		t.Fatal("revision guard migration is not registered")
	}
	up, err := migrationsFS.ReadFile("migrations/054_transcript_revision_guard.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(up), "CREATE TRIGGER trg_transcript_writer_version_revision_aware") ||
		!strings.Contains(string(up), "CREATE TRIGGER trg_transcript_writer_version_revision_guard") {
		t.Fatal("migration 054 must create the signal trigger and the guard trigger")
	}
	down, err := migrationsFS.ReadFile("migrations/054_transcript_revision_guard.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "RAISE EXCEPTION") {
		t.Fatal("the revision guard down migration must refuse: the migration is forward-only")
	}
}
