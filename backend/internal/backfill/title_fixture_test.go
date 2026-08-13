package backfill

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"testing"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/title_backfill/cases.yaml
var titleBackfillCasesYAML []byte

type titleBackfillFixture struct {
	Name      string `yaml:"name"`
	Arm       string `yaml:"arm"`
	Mode      string `yaml:"mode"`
	Title     string `yaml:"title"`
	Generated string `yaml:"generated"`
	FirstUser string `yaml:"first_user"`
	Expected  string `yaml:"expected"`
}

func loadTitleBackfillFixtures(data []byte) ([]titleBackfillFixture, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var cases []titleBackfillFixture
	if err := decoder.Decode(&cases); err != nil {
		return nil, fmt.Errorf("decode strict title backfill fixture: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("strict title backfill fixture must contain exactly one YAML document")
	}
	if len(cases) != 10 {
		return nil, fmt.Errorf("strict title backfill fixture has %d rows, want 10", len(cases))
	}
	names, arms := map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Arm == "" {
			return nil, fmt.Errorf("strict title backfill fixture requires non-empty name and arm")
		}
		if names[c.Name] {
			return nil, fmt.Errorf("strict title backfill fixture repeats name %q", c.Name)
		}
		names[c.Name], arms[c.Arm] = true, true
		if _, err := ParseTitleBackfillMode(c.Mode); err != nil {
			return nil, err
		}
	}
	for _, arm := range []string{"preserve", "sanitize", "sanitize-generated", "generated-candidate", "derive-current", "derive-bare", "derive-jsonl", "malformed", "concurrent-skip", "idempotent"} {
		if !arms[arm] {
			return nil, fmt.Errorf("strict title backfill fixture omits required arm %q", arm)
		}
	}
	if !names["shared_project_path_parity"] {
		return nil, fmt.Errorf("strict title backfill fixture omits shared_project_path_parity")
	}
	return cases, nil
}

func TestTitleBackfillFixtureAndModeContract(t *testing.T) {
	cases, err := loadTitleBackfillFixtures(titleBackfillCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if tc.Name != "shared_project_path_parity" {
			continue
		}
		result, err := pipeline.Generate(tc.FirstUser, redact.TitleContext{Harness: schema.HarnessClaudeCode, ProjectPath: "/Users/alice/work/app"})
		if err != nil {
			t.Fatal(err)
		}
		if result.Text != tc.Expected {
			t.Fatalf("parity title = %q, want %q", result.Text, tc.Expected)
		}
	}
	mutated := bytes.Replace(titleBackfillCasesYAML, []byte("mode: apply"), []byte("mode: unexpected"), 1)
	if _, err := loadTitleBackfillFixtures(mutated); err == nil {
		t.Fatal("mutated unknown mode passed strict fixture loader")
	}
	if _, err := ParseTitleBackfillMode(""); err == nil {
		t.Fatal("empty mode accepted")
	}
}
