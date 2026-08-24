package sessionorigin

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/classification/cases.yaml
var classificationCasesYAML []byte

// classificationTurnRun is one run of consecutive turns that share a role and
// content. Runs keep the production shapes (19 assistant turns, 14 tool turns)
// declarative instead of pasting hundreds of YAML nodes.
type classificationTurnRun struct {
	Role    string `yaml:"role"`
	Content string `yaml:"content"`
	Count   int    `yaml:"count"`
}

type classificationFixture struct {
	Name string `yaml:"name"`
	Arm  string `yaml:"arm"`
	// NilPayload describes the "no readable content at all" input; such a row
	// carries no turns and asserts the nil branch of Classify.
	NilPayload bool                    `yaml:"nil_payload"`
	Turns      []classificationTurnRun `yaml:"turns"`
	Expected   string                  `yaml:"expected"`
}

// wantClassificationRows is the exact fixture row count. A row deleted or
// added without updating this number fails the loader, so coverage cannot
// silently shrink.
const wantClassificationRows = 14

// requiredClassificationArms is every behaviour the rule must keep proving.
var requiredClassificationArms = []string{
	"real-user-prompt",
	"command-invocation-as-system-turn",
	"command-invocation-as-user-turn",
	"command-invocation-with-attributes",
	"agent-work-without-prompt",
	"system-only-stays-visible",
	"command-only-is-a-person",
	"no-turns-at-all",
	"nil-payload",
	"blank-user-turn-is-not-a-prompt",
	"prompt-found-late-in-order",
	"tool-work-alone-counts",
	"command-must-open-the-turn",
	"unlisted-wrapper-is-not-a-command",
}

func loadClassificationFixtures(data []byte) ([]classificationFixture, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var cases []classificationFixture
	if err := decoder.Decode(&cases); err != nil {
		return nil, fmt.Errorf("decode strict session-origin classification fixture: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("strict session-origin classification fixture must contain exactly one YAML document")
	}
	if len(cases) != wantClassificationRows {
		return nil, fmt.Errorf("strict session-origin classification fixture has %d rows, want %d", len(cases), wantClassificationRows)
	}
	names, arms := map[string]bool{}, map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Arm == "" {
			return nil, fmt.Errorf("strict session-origin classification fixture requires non-empty name and arm")
		}
		if names[c.Name] {
			return nil, fmt.Errorf("strict session-origin classification fixture repeats name %q", c.Name)
		}
		names[c.Name], arms[c.Arm] = true, true
		if _, err := Parse(c.Expected); err != nil {
			return nil, fmt.Errorf("row %q: %w", c.Name, err)
		}
		if c.NilPayload && len(c.Turns) != 0 {
			return nil, fmt.Errorf("row %q declares nil_payload and turns at once; a nil payload has no turns", c.Name)
		}
		for _, run := range c.Turns {
			if run.Count < 1 {
				return nil, fmt.Errorf("row %q has a turn run with count %d; a run must describe at least one turn", c.Name, run.Count)
			}
			if !schema.Role(run.Role).IsValid() {
				return nil, fmt.Errorf("row %q has turn role %q outside the contract role menu", c.Name, run.Role)
			}
		}
	}
	for _, arm := range requiredClassificationArms {
		if !arms[arm] {
			return nil, fmt.Errorf("strict session-origin classification fixture omits required arm %q", arm)
		}
	}
	// The two production-shape rows exist to prove the rule survives a long,
	// realistic session, so each must keep both kinds of work in quantity. They
	// are the same session length and differ only in the one turn that says who
	// started it, so the row that classifies user cannot be passing for some
	// other reason than the command invocation.
	for _, c := range cases {
		if c.Arm != "agent-work-without-prompt" && c.Arm != "command-invocation-as-system-turn" {
			continue
		}
		assistant, tool := 0, 0
		for _, run := range c.Turns {
			switch schema.Role(run.Role) {
			case schema.RoleAssistant:
				assistant += run.Count
			case schema.RoleTool:
				tool += run.Count
			}
		}
		if assistant < 2 || tool < 2 {
			return nil, fmt.Errorf("row %q must keep multiple assistant and tool turns; got assistant=%d tool=%d", c.Name, assistant, tool)
		}
	}
	return cases, nil
}

func (c classificationFixture) payload() *schema.SessionDetailPayload {
	if c.NilPayload {
		return nil
	}
	payload := &schema.SessionDetailPayload{ID: c.Name, Harness: schema.HarnessClaudeCode}
	index := 0
	for _, run := range c.Turns {
		for range run.Count {
			payload.Turns = append(payload.Turns, schema.TurnDetail{
				Index:     index,
				Role:      schema.Role(run.Role),
				Content:   run.Content,
				Timestamp: time.Date(2026, 8, 24, 9, 0, index, 0, time.UTC),
			})
			index++
		}
	}
	payload.TurnCount = index
	return payload
}

func TestClassifyFixtures(t *testing.T) {
	cases, err := loadClassificationFixtures(classificationCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got := Classify(c.payload())
			if err := got.Validate(); err != nil {
				t.Fatalf("Classify returned a value outside the menu: %v", err)
			}
			if string(got) != c.Expected {
				t.Fatalf("Classify = %q, want %q (arm %q)", got, c.Expected, c.Arm)
			}
		})
	}
}

func TestOriginValidateFailsClosedOnUnknownValue(t *testing.T) {
	for _, value := range []string{"", "USER", "subagent", "agent "} {
		if _, err := Parse(value); err == nil {
			t.Fatalf("Parse(%q) accepted a value outside the menu", value)
		}
	}
	for _, origin := range All {
		if err := origin.Validate(); err != nil {
			t.Fatalf("menu member %q rejected: %v", origin, err)
		}
	}
}
