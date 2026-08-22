package backfill

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
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
	LeadingNonUserTurns   int      `yaml:"leading_non_user_turns"`
	FirstUserTurns        []string `yaml:"first_user_turns"`
	Expected              string   `yaml:"expected"`
	ExpectedErrorContains string   `yaml:"expected_error_contains"`
	// ExpectedWarnTurnIndex, when set, is the original-payload turn index a
	// skipped-with-error candidate turn must be logged under. It is nil
	// (unset) for every row that does not assert on log output.
	ExpectedWarnTurnIndex *int `yaml:"expected_warn_turn_index"`
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
	if len(cases) != 15 {
		return nil, fmt.Errorf("strict title backfill fixture has %d rows, want 15", len(cases))
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
	for _, arm := range []string{"preserve", "sanitize", "sanitize-generated", "generated-candidate", "derive-current", "derive-bare", "derive-jsonl", "malformed", "concurrent-skip", "idempotent", "derive-caveat-then-prose", "derive-bare-command-then-prose", "derive-malformed-then-prose", "derive-only-injected", "derive-interleaved-turn-index"} {
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

// buildUserTurnsPayload builds a minimal payload whose turns are `leading`
// non-user (assistant) turns followed by the supplied ordered RoleUser turn
// contents, matching what a fixture row's leading_non_user_turns and
// first_user_turns describe together. Every existing fixture row omits
// leading_non_user_turns, so it defaults to 0 and the payload is exactly the
// RoleUser turns in order, as before.
func buildUserTurnsPayload(leading int, turns []string) *schema.SessionDetailPayload {
	details := make([]schema.TurnDetail, 0, leading+len(turns))
	for i := 0; i < leading; i++ {
		details = append(details, schema.TurnDetail{Index: i, Role: schema.RoleAssistant, Content: "acknowledged"})
	}
	for i, content := range turns {
		details = append(details, schema.TurnDetail{Index: leading + i, Role: schema.RoleUser, Content: content})
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
			payload := buildUserTurnsPayload(tc.LeadingNonUserTurns, tc.FirstUserTurns)
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

// TestDeriveTitleFromPayloadLogsOriginalPayloadTurnIndex proves the warn log
// for a skipped-with-error candidate turn names the turn's index in the
// original payload, not its position among the filtered RoleUser turns.
// assistant_turn_then_malformed_then_prose places one non-RoleUser turn
// before the malformed candidate, so the two positions differ (payload index
// 1 vs filtered-list position 0); asserting on the captured log output
// catches a regression that logs the filtered-list position instead of the
// original index.
func TestDeriveTitleFromPayloadLogsOriginalPayloadTurnIndex(t *testing.T) {
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
		if tc.ExpectedWarnTurnIndex == nil {
			continue
		}
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			ran++
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			payload := buildUserTurnsPayload(tc.LeadingNonUserTurns, tc.FirstUserTurns)
			title, err := deriveTitleFromPayload(payload, pipeline, ctx, logger)
			if err != nil || title != tc.Expected {
				t.Fatalf("derive title = %q err=%v, want %q/nil", title, err, tc.Expected)
			}
			want := fmt.Sprintf("turn_index=%d", *tc.ExpectedWarnTurnIndex)
			if !strings.Contains(logs.String(), want) {
				t.Fatalf("warn log = %q, want it to contain %q (the original payload turn index)", logs.String(), want)
			}
			if *tc.ExpectedWarnTurnIndex != 0 && strings.Contains(logs.String(), "turn_index=0") {
				t.Fatalf("warn log = %q, wrongly names the filtered-user-turn-list position turn_index=0 instead of the original payload index %d", logs.String(), *tc.ExpectedWarnTurnIndex)
			}
		})
	}
	if ran == 0 {
		t.Fatal("no fixture row set expected_warn_turn_index; the turn_index attribution was not exercised")
	}
}

// callRecordingPipeline wraps the real title pipeline and records, in call
// order, every turn text passed to Generate. It lets a test assert the
// production selection loop in deriveTitleFromPayload actually visits a
// later candidate turn instead of stopping after the first one, without
// needing a second, separately swappable selection contract.
type callRecordingPipeline struct {
	*redact.TitlePipeline
	calls []string
}

func (p *callRecordingPipeline) Generate(turn string, context redact.TitleContext) (redact.TitleResult, error) {
	p.calls = append(p.calls, turn)
	return p.TitlePipeline.Generate(turn, context)
}

// TestDeriveTitleFromPayloadVisitsEveryCandidateTurn proves the multi-turn
// fixture rows are load-bearing: deriveTitleFromPayload must call Generate
// once for every candidate turn up to and including the winner, in every
// fixture row the winning turn is the last candidate, so the call count must
// equal the candidate count. A selection loop that regresses to evaluating
// only the first candidate turn (the pre-fix behavior) would call Generate
// exactly once regardless of how many candidate turns a row has, and this
// test would fail.
func TestDeriveTitleFromPayloadVisitsEveryCandidateTurn(t *testing.T) {
	cases, err := loadTitleBackfillFixtures(titleBackfillCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	real, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatal(err)
	}
	ctx := redact.TitleContext{Harness: schema.HarnessClaudeCode, ProjectPath: "/Users/developer/work/sample-app"}
	ran := 0
	for _, tc := range cases {
		if len(tc.FirstUserTurns) == 0 || tc.ExpectedErrorContains != "" {
			continue
		}
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			ran++
			recorder := &callRecordingPipeline{TitlePipeline: real}
			payload := buildUserTurnsPayload(tc.LeadingNonUserTurns, tc.FirstUserTurns)
			title, err := deriveTitleFromPayload(payload, recorder, ctx, nil)
			if err != nil || title != tc.Expected {
				t.Fatalf("derive title = %q err=%v, want %q/nil", title, err, tc.Expected)
			}
			if len(recorder.calls) != len(tc.FirstUserTurns) {
				t.Fatalf("row %q called Generate %d time(s) for %d candidate turns; a selection loop that stops after the first turn (the pre-fix regression) would call Generate exactly once regardless of turn count", tc.Name, len(recorder.calls), len(tc.FirstUserTurns))
			}
		})
	}
	if ran == 0 {
		t.Fatal("no multi-candidate fixture row exercised the call-recording regression guard")
	}
}
