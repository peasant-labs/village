package backfill

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/title_backfill/cases.yaml
var titleBackfillCasesYAML []byte

type titleBackfillFixture struct {
	Name                  string   `yaml:"name"`
	Arm                   string   `yaml:"arm"`
	Mode                  string   `yaml:"mode"`
	Title                 string   `yaml:"title"`
	Generated             string   `yaml:"generated"`
	FirstUserTurns        []string `yaml:"first_user_turns"`
	Expected              string   `yaml:"expected"`
	ExpectedErrorContains string   `yaml:"expected_error_contains"`
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
	if len(cases) != 14 {
		return nil, fmt.Errorf("strict title backfill fixture has %d rows, want 14", len(cases))
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
	for _, arm := range []string{"preserve", "sanitize", "sanitize-generated", "generated-candidate", "derive-current", "derive-bare", "derive-jsonl", "malformed", "concurrent-skip", "idempotent", "derive-caveat-then-prose", "derive-bare-command-then-prose", "derive-malformed-then-prose", "derive-only-injected"} {
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
		result, err := pipeline.Generate(tc.FirstUserTurns[0], redact.TitleContext{Harness: schema.HarnessClaudeCode, ProjectPath: "/Users/developer/work/sample-app"})
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

// buildUserTurnsPayload builds a minimal payload whose turns are exactly the
// supplied ordered RoleUser turn contents, matching what a fixture row's
// first_user_turns describes.
func buildUserTurnsPayload(turns []string) *schema.SessionDetailPayload {
	details := make([]schema.TurnDetail, 0, len(turns))
	for i, content := range turns {
		details = append(details, schema.TurnDetail{Index: i, Role: schema.RoleUser, Content: content})
	}
	return &schema.SessionDetailPayload{Turns: details}
}

// TestDeriveTitleFromPayloadFixtures drives every fixture row that carries
// first_user_turns through the pure selection function directly (no pool, no
// blob storage), proving the multi-turn injected-then-prose selection and the
// only-injected error path against real fixture content rather than shape
// alone.
func TestDeriveTitleFromPayloadFixtures(t *testing.T) {
	cases, err := loadTitleBackfillFixtures(titleBackfillCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatal(err)
	}
	ctx := redact.TitleContext{Harness: schema.HarnessClaudeCode, ProjectPath: "/Users/developer/work/sample-app"}
	ran := 0
	for _, tc := range cases {
		if len(tc.FirstUserTurns) == 0 {
			continue
		}
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			ran++
			payload := buildUserTurnsPayload(tc.FirstUserTurns)
			title, err := deriveTitleFromPayload(payload, pipeline, ctx, nil)
			if tc.ExpectedErrorContains != "" {
				if err == nil || !strings.Contains(err.Error(), tc.ExpectedErrorContains) {
					t.Fatalf("derive error = %v, want containing %q", err, tc.ExpectedErrorContains)
				}
				if title != "" {
					t.Fatalf("derive title = %q on the error path, want empty", title)
				}
				return
			}
			if err != nil {
				t.Fatalf("derive error = %v, want nil", err)
			}
			if title != tc.Expected {
				t.Fatalf("derive title = %q, want %q", title, tc.Expected)
			}
			for _, skippedTurn := range tc.FirstUserTurns[:len(tc.FirstUserTurns)-1] {
				if strings.Contains(title, skippedTurn) {
					t.Fatalf("derived title leaked raw text from a skipped turn %q: %q", skippedTurn, title)
				}
			}
		})
	}
	if ran == 0 {
		t.Fatal("no derive-* fixture row carried first_user_turns; the pure function was not exercised")
	}
}

// firstTurnOnlyPipeline reproduces the pre-fix backfill.derive behavior: it
// only ever evaluates the first candidate turn and never tries a later one,
// whether the first turn errors or cleans to empty text. Wrapping it around
// the real pipeline lets the fixture-driven mutation proof below run genuine
// redaction/cleaning logic while only mutating the turn-selection contract.
type firstTurnOnlyPipeline struct{ *redact.TitlePipeline }

func (p firstTurnOnlyPipeline) GenerateFromTurns(turns []string, context redact.TitleContext) (redact.TitleResult, int, []error) {
	if len(turns) == 0 {
		return redact.TitleResult{}, -1, nil
	}
	result, err := p.Generate(turns[0], context)
	if err != nil {
		return redact.TitleResult{}, -1, []error{err}
	}
	if result.Text == "" {
		return redact.TitleResult{}, -1, nil
	}
	return result, 0, nil
}

// TestDeriveTitleFromPayloadMutationDetectsFirstTurnOnlyRegression proves the
// multi-turn fixture rows are load-bearing: swapping in the old
// first-turn-only selection behavior must break every multi-turn row (the
// second, prose turn is never reached) while leaving the single-turn row
// unaffected. If a future change silently reverts GenerateFromTurns callers
// back to first-turn-only selection, this test fails.
func TestDeriveTitleFromPayloadMutationDetectsFirstTurnOnlyRegression(t *testing.T) {
	cases, err := loadTitleBackfillFixtures(titleBackfillCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	real, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatal(err)
	}
	mutated := firstTurnOnlyPipeline{real}
	ctx := redact.TitleContext{Harness: schema.HarnessClaudeCode, ProjectPath: "/Users/developer/work/sample-app"}

	multiTurnRows := map[string]bool{"caveat_then_prose": true, "bare_command_then_prose": true, "malformed_then_prose": true}
	singleTurnRows := map[string]bool{"shared_project_path_parity": true}
	exercisedMulti, exercisedSingle := 0, 0

	for _, tc := range cases {
		if len(tc.FirstUserTurns) == 0 || tc.ExpectedErrorContains != "" {
			continue
		}
		payload := buildUserTurnsPayload(tc.FirstUserTurns)
		title, err := deriveTitleFromPayload(payload, mutated, ctx, nil)
		matches := err == nil && title == tc.Expected
		switch {
		case multiTurnRows[tc.Name]:
			exercisedMulti++
			if matches {
				t.Fatalf("row %q matched expected %q under the old first-turn-only selection; the mutation should have broken this multi-turn row", tc.Name, tc.Expected)
			}
		case singleTurnRows[tc.Name]:
			exercisedSingle++
			if !matches {
				t.Fatalf("row %q = %q (err=%v) under first-turn-only selection, want %q/nil; a single-turn row must be unaffected by the multi-turn regression", tc.Name, title, err, tc.Expected)
			}
		}
	}
	if exercisedMulti != len(multiTurnRows) || exercisedSingle != len(singleTurnRows) {
		t.Fatalf("mutation coverage incomplete: multi-turn=%d/%d single-turn=%d/%d", exercisedMulti, len(multiTurnRows), exercisedSingle, len(singleTurnRows))
	}
}
