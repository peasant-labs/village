package backfill

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/origin_backfill/cases.yaml
var originBackfillCasesYAML []byte

type originTurnRun struct {
	Role    string `yaml:"role"`
	Content string `yaml:"content"`
	Count   int    `yaml:"count"`
}

type originBackfillFixture struct {
	Name           string          `yaml:"name"`
	Arm            string          `yaml:"arm"`
	StoredOrigin   string          `yaml:"stored_origin"`
	Turns          []originTurnRun `yaml:"turns"`
	ExpectedOrigin string          `yaml:"expected_origin"`
	Outcome        string          `yaml:"outcome"`
	ReadFails      bool            `yaml:"read_fails"`
}

// wantOriginBackfillRows is the exact fixture row count; a deleted row fails
// the loader instead of silently shrinking coverage.
const wantOriginBackfillRows = 6

var requiredOriginBackfillArms = []string{
	"reclassify-to-agent",
	"reclassify-to-user",
	"system-only-is-a-no-op",
	"idempotent-agent",
	"corrects-a-wrong-stored-value",
	"read-failure-leaves-the-row-alone",
}

var originBackfillOutcomes = map[string]bool{"updated": true, "unchanged": true, "failed": true}

func loadOriginBackfillFixtures(data []byte) ([]originBackfillFixture, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var cases []originBackfillFixture
	if err := decoder.Decode(&cases); err != nil {
		return nil, fmt.Errorf("decode strict origin backfill fixture: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("strict origin backfill fixture must contain exactly one YAML document")
	}
	if len(cases) != wantOriginBackfillRows {
		return nil, fmt.Errorf("strict origin backfill fixture has %d rows, want %d", len(cases), wantOriginBackfillRows)
	}
	names, arms := map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Arm == "" {
			return nil, fmt.Errorf("strict origin backfill fixture requires non-empty name and arm")
		}
		if names[c.Name] {
			return nil, fmt.Errorf("strict origin backfill fixture repeats name %q", c.Name)
		}
		names[c.Name], arms[c.Arm] = true, true
		if _, err := sessionorigin.Parse(c.StoredOrigin); err != nil {
			return nil, fmt.Errorf("row %q stored origin: %w", c.Name, err)
		}
		if _, err := sessionorigin.Parse(c.ExpectedOrigin); err != nil {
			return nil, fmt.Errorf("row %q expected origin: %w", c.Name, err)
		}
		if !originBackfillOutcomes[c.Outcome] {
			return nil, fmt.Errorf("row %q has outcome %q; expected one of updated, unchanged, failed", c.Name, c.Outcome)
		}
		if c.Outcome == "unchanged" && c.StoredOrigin != c.ExpectedOrigin {
			return nil, fmt.Errorf("row %q claims no change while moving from %q to %q", c.Name, c.StoredOrigin, c.ExpectedOrigin)
		}
		if c.Outcome == "updated" && c.StoredOrigin == c.ExpectedOrigin {
			return nil, fmt.Errorf("row %q claims an update while staying at %q", c.Name, c.StoredOrigin)
		}
		if c.Outcome == "failed" && c.StoredOrigin != c.ExpectedOrigin {
			return nil, fmt.Errorf("row %q fails but expects the stored value to move from %q to %q; a failed row must be left exactly as it was", c.Name, c.StoredOrigin, c.ExpectedOrigin)
		}
		if c.ReadFails != (c.Outcome == "failed") {
			return nil, fmt.Errorf("row %q pairs read_fails=%v with outcome %q; the injected read failure is what makes a row fail", c.Name, c.ReadFails, c.Outcome)
		}
		for _, run := range c.Turns {
			if run.Count < 1 {
				return nil, fmt.Errorf("row %q has a turn run with count %d; a run describes at least one turn", c.Name, run.Count)
			}
			if !schema.Role(run.Role).IsValid() {
				return nil, fmt.Errorf("row %q has turn role %q outside the contract role menu", c.Name, run.Role)
			}
		}
	}
	for _, arm := range requiredOriginBackfillArms {
		if !arms[arm] {
			return nil, fmt.Errorf("strict origin backfill fixture omits required arm %q", arm)
		}
	}
	return cases, nil
}

// rawPayload renders the fixture turns as the stored canonical envelope the
// production migrate-on-read boundary accepts.
func (c originBackfillFixture) rawPayload(t *testing.T) []byte {
	t.Helper()
	type turnJSON struct {
		Index   int    `json:"index"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	details := []turnJSON{}
	for _, run := range c.Turns {
		for range run.Count {
			details = append(details, turnJSON{Index: len(details), Role: run.Role, Content: run.Content})
		}
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(fmt.Sprintf(`{"contractVersion":"0.1.1","kind":"session_detail","sessionDetail":{"id":"origin-fixture","harness":"claude-code","turns":%s}}`, encoded))
}

func TestOriginBackfillFixtureLoads(t *testing.T) {
	cases, err := loadOriginBackfillFixtures(originBackfillCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != wantOriginBackfillRows {
		t.Fatalf("rows=%d want %d", len(cases), wantOriginBackfillRows)
	}
}

func TestParseOriginBackfillMode(t *testing.T) {
	for value, want := range map[string]OriginBackfillMode{"dry-run": OriginBackfillModeDryRun, "apply": OriginBackfillModeApply} {
		got, err := ParseOriginBackfillMode(value)
		if err != nil || got != want {
			t.Fatalf("ParseOriginBackfillMode(%q) = %v, %v", value, got, err)
		}
	}
	for _, value := range []string{"", "Apply", "reclassify-everything"} {
		got, err := ParseOriginBackfillMode(value)
		if err == nil {
			t.Fatalf("ParseOriginBackfillMode(%q) accepted an unsupported mode as %v", value, got)
		}
		if bytes.Contains([]byte(err.Error()), []byte(value)) && value != "" {
			t.Fatalf("ParseOriginBackfillMode echoed the operator value %q back in its error", value)
		}
	}
}
