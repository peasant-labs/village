package backfill

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
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
	// ProjectPath is the recorded project root the row's title is redacted
	// against, empty when the row records no project. It lives on the row so
	// the input that produces Expected travels with the expectation itself.
	ProjectPath           string   `yaml:"project_path"`
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
	for _, arm := range []string{"preserve", "sanitize", "sanitize-generated", "generated-candidate", "derive-current", "derive-bare", "derive-jsonl", "malformed", "concurrent-skip", "idempotent", "derive-caveat-then-prose", "derive-bare-command-then-prose", "derive-malformed-then-prose", "derive-only-injected", "derive-interleaved-turn-index", "derive-markup-title"} {
		if !arms[arm] {
			return nil, fmt.Errorf("strict title backfill fixture omits required arm %q", arm)
		}
	}
	if err := assertExactTitleBackfillNames(names); err != nil {
		return nil, err
	}
	// multiCandidateArms are the arms whose entire point is proving the
	// selection loop visits a later candidate turn (injected-then-prose,
	// only-injected, interleaved-turn-index). A row filed under one of these
	// arms with fewer than two first_user_turns can no longer exercise that
	// behavior, so a future edit that shrinks a row to one turn must fail
	// the fixture loader instead of silently degrading every guard that
	// depends on multi-turn coverage to a vacuous pass.
	multiCandidateArms := map[string]bool{
		"derive-markup-title":            true,
		"derive-caveat-then-prose":       true,
		"derive-bare-command-then-prose": true,
		"derive-malformed-then-prose":    true,
		"derive-only-injected":           true,
		"derive-interleaved-turn-index":  true,
	}
	for _, c := range cases {
		if multiCandidateArms[c.Arm] && len(c.FirstUserTurns) < 2 {
			return nil, fmt.Errorf("strict title backfill fixture row %q (arm %q) carries %d first_user_turns, want at least 2: this arm proves the selection loop visits a later candidate turn and a single-turn row cannot exercise that", c.Name, c.Arm, len(c.FirstUserTurns))
		}
	}
	return cases, nil
}

// requiredTitleBackfillCaseNames is the deletion-protection manifest for
// title_backfill/cases.yaml, asserted as EXACT membership in both directions.
//
// It replaces a bare row count. A count cannot say WHICH behavior stopped being
// covered when it moves, goes stale on every legitimate addition, and two
// branches that each add a case collide on the same integer. These names say
// what must not be lost: the keep/sanitize/derive decision arms, the whole
// injected-then-prose turn-selection family, and the operational arms
// (concurrent edit, idempotent re-apply, malformed content).
var requiredTitleBackfillCaseNames = []string{
	"safe_manual_preserved",
	"unsafe_manual_sanitized",
	"unsafe_generated_sanitized",
	"missing_title_uses_generated",
	"shared_project_path_parity",
	"caveat_then_prose",
	"bare_command_then_prose",
	"malformed_then_prose",
	"only_injected",
	"assistant_turn_then_malformed_then_prose",
	"bare_payload_derivation",
	"raw_jsonl_derivation",
	"malformed_content_continues",
	"concurrent_edit_skips",
	"second_apply_idempotent",
	"markup_title_rederived",
	"markup_generated_not_promoted",
}

// assertExactTitleBackfillNames holds the fixture's case names to exactly the
// manifest. A missing name means a case was deleted; an unexpected name means a
// case was added without recording why it must survive a later edit. Both are
// reported by NAME so the failure says what changed.
func assertExactTitleBackfillNames(present map[string]bool) error {
	want := map[string]bool{}
	for _, name := range requiredTitleBackfillCaseNames {
		want[name] = true
	}
	var missing, unexpected []string
	for name := range want {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	for name := range present {
		if !want[name] {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	if len(missing) > 0 {
		return fmt.Errorf("the title backfill fixture no longer contains %v. Each of those cases exists because "+
			"losing it hides a real failure; restore the row rather than removing it from the manifest", missing)
	}
	if len(unexpected) > 0 {
		return fmt.Errorf("the title backfill fixture carries %v, which the manifest does not list. Add each new "+
			"case to requiredTitleBackfillCaseNames so a later deletion is caught by name", unexpected)
	}
	return nil
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
		result, err := pipeline.Generate(tc.FirstUserTurns[0], redact.TitleContext{Harness: schema.HarnessClaudeCode, ProjectPath: tc.ProjectPath})
		if err != nil {
			t.Fatal(err)
		}
		if result.Text != tc.Expected {
			t.Fatalf("parity title = %q, want %q", result.Text, tc.Expected)
		}
	}
	// The keep/re-derive gate: a preserve-arm stored title is owner prose and
	// must never trigger re-derivation; a derive-markup-title-arm stored title
	// or stored generated title carries recognized harness markup and must.
	gateChecked := 0
	for _, tc := range cases {
		switch tc.Arm {
		case "preserve":
			if tc.Title != "" && titleNeedsRederivation(tc.Title, schema.HarnessClaudeCode) {
				t.Fatalf("preserve row %q: owner prose title %q classified as needing re-derivation", tc.Name, tc.Title)
			}
			gateChecked++
		case "derive-markup-title":
			stored := tc.Title
			if stored == "" {
				stored = tc.Generated
			}
			if stored == "" {
				t.Fatalf("derive-markup-title row %q carries neither a stored title nor a stored generated title; the row cannot exercise the markup gate", tc.Name)
			}
			if !titleNeedsRederivation(stored, schema.HarnessClaudeCode) {
				t.Fatalf("derive-markup-title row %q: markup title %q not classified as needing re-derivation", tc.Name, stored)
			}
			gateChecked++
		}
	}
	if gateChecked < 3 {
		t.Fatalf("keep/re-derive gate exercised by %d rows, want at least 3 (one preserve and two derive-markup-title)", gateChecked)
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
	ran := 0
	for _, tc := range cases {
		if len(tc.FirstUserTurns) == 0 {
			continue
		}
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			ran++
			ctx := redact.TitleContext{Harness: schema.HarnessClaudeCode, ProjectPath: tc.ProjectPath}
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
			ctx := redact.TitleContext{Harness: schema.HarnessClaudeCode, ProjectPath: tc.ProjectPath}
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
	ran := 0
	for _, tc := range cases {
		if len(tc.FirstUserTurns) == 0 || tc.ExpectedErrorContains != "" {
			continue
		}
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			if len(tc.FirstUserTurns) >= 2 {
				ran++
			}
			recorder := &callRecordingPipeline{TitlePipeline: real}
			ctx := redact.TitleContext{Harness: schema.HarnessClaudeCode, ProjectPath: tc.ProjectPath}
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
