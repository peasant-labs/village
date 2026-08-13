package testfixtures

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAdversarialFixtures(t *testing.T) {
	fixtures, err := LoadAdversarialFixtures("testdata/fixtures/adversarial.yaml")
	if err != nil {
		t.Fatalf("failed to load adversarial fixtures: %v", err)
	}

	if len(fixtures.InjectionPayloads) != 9 {
		t.Errorf("expected 9 injection payloads, got %d", len(fixtures.InjectionPayloads))
	}

	if len(fixtures.BoundaryPayloads) != 8 {
		t.Errorf("expected 8 boundary payloads, got %d", len(fixtures.BoundaryPayloads))
	}

	if len(fixtures.JSONPayloads) != 6 {
		t.Errorf("expected 6 JSON payloads, got %d", len(fixtures.JSONPayloads))
	}

	if len(fixtures.SpecialValues) != 8 {
		t.Errorf("expected 8 special values, got %d", len(fixtures.SpecialValues))
	}
}

func TestLoadQualityFixtures(t *testing.T) {
	fixtures, err := LoadQualityFixtures("testdata/fixtures/quality.yaml")
	if err != nil {
		t.Fatalf("failed to load quality fixtures: %v", err)
	}

	if len(fixtures.TitleGenerated) != 5 {
		t.Errorf("expected 5 title generated entries, got %d", len(fixtures.TitleGenerated))
	}

	if len(fixtures.Outcomes) != 5 {
		t.Errorf("expected 5 outcome entries, got %d", len(fixtures.Outcomes))
	}

	if len(fixtures.FilesTouched) == 0 {
		t.Error("expected non-empty files_touched")
	}

	if len(fixtures.LinesChanged) == 0 {
		t.Error("expected non-empty lines_changed")
	}

	if len(fixtures.Ratios) == 0 {
		t.Error("expected non-empty ratios")
	}

	if len(fixtures.Counts) == 0 {
		t.Error("expected non-empty counts")
	}

	if len(fixtures.Booleans) == 0 {
		t.Error("expected non-empty booleans")
	}

	if len(fixtures.ComputedAt) == 0 {
		t.Error("expected non-empty computed_at")
	}

	if len(fixtures.ComputeVersions) == 0 {
		t.Error("expected non-empty compute_versions")
	}
}

func TestLoadStatsFixtures(t *testing.T) {
	fixtures, err := LoadStatsFixtures("testdata/fixtures/stats.yaml")
	if err != nil {
		t.Fatalf("failed to load stats fixtures: %v", err)
	}

	if len(fixtures.TurnCounts) == 0 {
		t.Error("expected non-empty turn_counts")
	}

	if len(fixtures.ToolCallCounts) == 0 {
		t.Error("expected non-empty tool_call_counts")
	}

	if len(fixtures.SubagentCounts) == 0 {
		t.Error("expected non-empty subagent_counts")
	}

	if len(fixtures.DurationMs) == 0 {
		t.Error("expected non-empty duration_ms")
	}

	if len(fixtures.TokensIn) == 0 {
		t.Error("expected non-empty tokens_in")
	}

	if len(fixtures.TokensOut) == 0 {
		t.Error("expected non-empty tokens_out")
	}
}

func TestLoadModelFixtures(t *testing.T) {
	fixtures, err := LoadModelFixtures("testdata/fixtures/models.yaml")
	if err != nil {
		t.Fatalf("failed to load model fixtures: %v", err)
	}

	if len(fixtures.Providers) == 0 {
		t.Error("expected non-empty providers")
	}

	if len(fixtures.Models) == 0 {
		t.Error("expected non-empty models")
	}

	if len(fixtures.HarnessVersions) == 0 {
		t.Error("expected non-empty harness_versions")
	}
}

func TestLoadTimestampFixtures(t *testing.T) {
	fixtures, err := LoadTimestampFixtures("testdata/fixtures/timestamps.yaml")
	if err != nil {
		t.Fatalf("failed to load timestamp fixtures: %v", err)
	}

	if len(fixtures.Timestamps) == 0 {
		t.Error("expected non-empty timestamps")
	}

	if len(fixtures.Ingested) == 0 {
		t.Error("expected non-empty ingested timestamps")
	}
}

func TestLoadSessionFixtures(t *testing.T) {
	fixtures, err := LoadSessionFixtures("testdata/fixtures/sessions.yaml")
	if err != nil {
		t.Fatalf("failed to load session fixtures: %v", err)
	}

	if len(fixtures.SessionIDs) == 0 {
		t.Error("expected non-empty session_ids")
	}

	if len(fixtures.ParentSessionIDs) == 0 {
		t.Error("expected non-empty parent_session_ids")
	}

	if len(fixtures.SchemaVersions) == 0 {
		t.Error("expected non-empty schema_versions")
	}
}

func TestLoadValidPayloads(t *testing.T) {
	payloads, err := LoadValidPayloads("testdata/valid/payloads.yaml")
	if err != nil {
		t.Fatalf("failed to load valid payloads: %v", err)
	}

	if len(payloads) == 0 {
		t.Error("expected non-empty valid payloads")
	}

	for _, p := range payloads {
		if p.Name == "" {
			t.Error("expected non-empty name in valid payload")
		}
		if p.Identity.SessionID == "" {
			t.Error("expected non-empty session ID in valid payload identity")
		}
		if p.Model.Provider == "" {
			t.Error("expected non-empty provider in valid payload model")
		}
		if p.Model.Model == "" {
			t.Error("expected non-empty model in valid payload model")
		}
	}
}

func TestLoadInvalidPayloads(t *testing.T) {
	payloads, err := LoadInvalidPayloads("testdata/invalid/payloads.yaml")
	if err != nil {
		t.Fatalf("failed to load invalid payloads: %v", err)
	}

	if len(payloads) == 0 {
		t.Error("expected non-empty invalid payloads")
	}

	for _, p := range payloads {
		if p.Name == "" {
			t.Error("expected non-empty name in invalid payload")
		}
		if p.ExpectedError == "" {
			t.Error("expected non-empty expectedError in invalid payload")
		}
	}
}

func TestAllFixtureFilesExist(t *testing.T) {
	fixtureFiles := []string{
		"testdata/fixtures/adversarial.yaml",
		"testdata/fixtures/quality.yaml",
		"testdata/fixtures/stats.yaml",
		"testdata/fixtures/models.yaml",
		"testdata/fixtures/timestamps.yaml",
		"testdata/fixtures/sessions.yaml",
		"testdata/valid/payloads.yaml",
		"testdata/invalid/payloads.yaml",
	}

	for _, f := range fixtureFiles {
		_, err := os.Stat(f)
		if err != nil {
			t.Errorf("fixture file %s does not exist: %v", f, err)
		}
	}
}

func TestLoadAllFixtures(t *testing.T) {
	testCases := []struct {
		name     string
		loadFunc func(string) (interface{}, error)
		path     string
	}{
		{"Adversarial", func(p string) (interface{}, error) { return LoadAdversarialFixtures(p) }, "testdata/fixtures/adversarial.yaml"},
		{"Quality", func(p string) (interface{}, error) { return LoadQualityFixtures(p) }, "testdata/fixtures/quality.yaml"},
		{"Stats", func(p string) (interface{}, error) { return LoadStatsFixtures(p) }, "testdata/fixtures/stats.yaml"},
		{"Models", func(p string) (interface{}, error) { return LoadModelFixtures(p) }, "testdata/fixtures/models.yaml"},
		{"Timestamps", func(p string) (interface{}, error) { return LoadTimestampFixtures(p) }, "testdata/fixtures/timestamps.yaml"},
		{"Sessions", func(p string) (interface{}, error) { return LoadSessionFixtures(p) }, "testdata/fixtures/sessions.yaml"},
		{"ValidPayloads", func(p string) (interface{}, error) { return LoadValidPayloads(p) }, "testdata/valid/payloads.yaml"},
		{"InvalidPayloads", func(p string) (interface{}, error) { return LoadInvalidPayloads(p) }, "testdata/invalid/payloads.yaml"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.loadFunc(tc.path)
			if err != nil {
				t.Errorf("failed to load %s fixtures: %v", tc.name, err)
			}
		})
	}
}

func TestFixturePaths(t *testing.T) {
	absPath, err := filepath.Abs("testdata/fixtures/quality.yaml")
	if err != nil {
		t.Fatalf("failed to get absolute path: %v", err)
	}

	fixtures, err := LoadQualityFixtures(absPath)
	if err != nil {
		t.Fatalf("failed to load fixtures with absolute path: %v", err)
	}

	if len(fixtures.TitleGenerated) == 0 {
		t.Error("expected non-empty title_generated when loading with absolute path")
	}
}

func TestCartesianProduct(t *testing.T) {
	a := []int{1, 2}
	b := []string{"x", "y", "z"}

	result := CartesianProduct2(a, b)

	if len(result) != 6 {
		t.Errorf("expected 6 combinations, got %d", len(result))
	}

	expected := []Tuple2[int, string]{
		{First: 1, Second: "x"},
		{First: 1, Second: "y"},
		{First: 1, Second: "z"},
		{First: 2, Second: "x"},
		{First: 2, Second: "y"},
		{First: 2, Second: "z"},
	}

	for i, exp := range expected {
		if result[i].First != exp.First || result[i].Second != exp.Second {
			t.Errorf("expected %v, got %v", exp, result[i])
		}
	}
}

func TestCartesianProduct3(t *testing.T) {
	sessions := []FixtureItem{{Name: "s1"}, {Name: "s2"}}
	timestamps := []TimestampRange{{Name: "t1"}, {Name: "t2"}}
	models := []FixtureItem{{Name: "m1"}, {Name: "m2"}, {Name: "m3"}}

	result := CartesianProduct3(sessions, timestamps, models)

	if len(result) != 12 {
		t.Errorf("expected 12 combinations (2*2*3), got %d", len(result))
	}

	if result[0].First.Name != "s1" || result[0].Second.Name != "t1" || result[0].Third.Name != "m1" {
		t.Errorf("first combination incorrect: %+v", result[0])
	}

	if result[11].First.Name != "s2" || result[11].Second.Name != "t2" || result[11].Third.Name != "m3" {
		t.Errorf("last combination incorrect: %+v", result[11])
	}
}

func TestCartesianProductEmpty(t *testing.T) {
	a := []int{1, 2}
	b := []int{}

	result := CartesianProduct2(a, b)

	if result != nil {
		t.Errorf("expected nil for empty slice, got %v", result)
	}

	result = CartesianProduct2([]int{}, b)
	if result != nil {
		t.Errorf("expected nil for empty first slice, got %v", result)
	}
}

func TestCartesianProductVariadic(t *testing.T) {
	slices := [][]int{{1, 2}, {3, 4}, {5, 6}}

	result := CartesianProduct(slices...)

	if len(result) != 8 {
		t.Errorf("expected 8 combinations (2*2*2), got %d", len(result))
	}

	if result[0][0] != 1 || result[0][1] != 3 || result[0][2] != 5 {
		t.Errorf("first combination incorrect: %v", result[0])
	}
}

func TestGenerateCombinatorialTestCases(t *testing.T) {
	fixturesByCategory := map[string][]FixtureItem{
		"providers": {{Name: "openai"}, {Name: "anthropic"}},
		"models":    {{Name: "gpt4"}, {Name: "claude"}},
	}

	categories := []string{"providers", "models"}
	result := GenerateCombinatorialTestCases(categories, fixturesByCategory)

	if len(result) != 4 {
		t.Errorf("expected 4 test cases (2*2), got %d", len(result))
	}

	if result[0].Name != "openai_gpt4" {
		t.Errorf("expected name 'openai_gpt4', got '%s'", result[0].Name)
	}

	if result[3].Name != "anthropic_claude" {
		t.Errorf("expected name 'anthropic_claude', got '%s'", result[3].Name)
	}
}
