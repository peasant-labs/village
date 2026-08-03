package testfixtures

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

type FixtureItem struct {
	Name        string      `json:"name" yaml:"name"`
	Value       interface{} `json:"value" yaml:"value"`
	Description string      `json:"description" yaml:"description"`
}

type TimestampRange struct {
	Name        string `json:"name" yaml:"name"`
	Start       int64  `json:"start" yaml:"start"`
	End         int64  `json:"end" yaml:"end"`
	Description string `json:"description" yaml:"description"`
}

type IngestedTimestamp struct {
	Name        string      `json:"name" yaml:"name"`
	Value       interface{} `json:"value" yaml:"value"`
	Description string      `json:"description" yaml:"description"`
}

type AdversarialInjection struct {
	Name        string `json:"name" yaml:"name"`
	Payload     string `json:"payload" yaml:"payload"`
	Category    string `json:"category" yaml:"category"`
	Description string `json:"description" yaml:"description"`
}

type AdversarialBoundary struct {
	Name        string      `json:"name" yaml:"name"`
	Value       interface{} `json:"value" yaml:"value"`
	Category    string      `json:"category" yaml:"category"`
	Description string      `json:"description" yaml:"description"`
}

type AdversarialJSON struct {
	Name        string `json:"name" yaml:"name"`
	Value       string `json:"value" yaml:"value"`
	Category    string `json:"category" yaml:"category"`
	Description string `json:"description" yaml:"description"`
}

type AdversarialSpecial struct {
	Name        string `json:"name" yaml:"name"`
	Value       string `json:"value" yaml:"value"`
	Category    string `json:"category" yaml:"category"`
	Description string `json:"description" yaml:"description"`
}

type AdversarialFixtures struct {
	InjectionPayloads []AdversarialInjection `json:"injection_payloads" yaml:"injection_payloads"`
	BoundaryPayloads  []AdversarialBoundary  `json:"boundary_payloads" yaml:"boundary_payloads"`
	JSONPayloads      []AdversarialJSON      `json:"json_payloads" yaml:"json_payloads"`
	SpecialValues     []AdversarialSpecial   `json:"special_values" yaml:"special_values"`
}

type QualityFixtures struct {
	TitleGenerated  []FixtureItem `json:"title_generated" yaml:"title_generated"`
	Outcomes        []FixtureItem `json:"outcomes" yaml:"outcomes"`
	FilesTouched    []FixtureItem `json:"files_touched" yaml:"files_touched"`
	LinesChanged    []FixtureItem `json:"lines_changed" yaml:"lines_changed"`
	Ratios          []FixtureItem `json:"ratios" yaml:"ratios"`
	Counts          []FixtureItem `json:"counts" yaml:"counts"`
	Booleans        []FixtureItem `json:"booleans" yaml:"booleans"`
	ComputedAt      []FixtureItem `json:"computed_at" yaml:"computed_at"`
	ComputeVersions []FixtureItem `json:"compute_versions" yaml:"compute_versions"`
}

type StatsFixtures struct {
	TurnCounts     []FixtureItem `json:"turn_counts" yaml:"turn_counts"`
	ToolCallCounts []FixtureItem `json:"tool_call_counts" yaml:"tool_call_counts"`
	SubagentCounts []FixtureItem `json:"subagent_counts" yaml:"subagent_counts"`
	DurationMs     []FixtureItem `json:"duration_ms" yaml:"duration_ms"`
	TokensIn       []FixtureItem `json:"tokens_in" yaml:"tokens_in"`
	TokensOut      []FixtureItem `json:"tokens_out" yaml:"tokens_out"`
}

type ModelFixtures struct {
	Providers       []FixtureItem `json:"providers" yaml:"providers"`
	Models          []FixtureItem `json:"models" yaml:"models"`
	HarnessVersions []FixtureItem `json:"harness_versions" yaml:"harness_versions"`
}

type TimestampFixtures struct {
	Timestamps []TimestampRange    `json:"timestamps" yaml:"timestamps"`
	Ingested   []IngestedTimestamp `json:"ingested" yaml:"ingested"`
}

type SessionFixtures struct {
	SessionIDs       []FixtureItem `json:"session_ids" yaml:"session_ids"`
	ParentSessionIDs []FixtureItem `json:"parent_session_ids" yaml:"parent_session_ids"`
	SchemaVersions   []FixtureItem `json:"schema_versions" yaml:"schema_versions"`
}

type Identity struct {
	SessionID       string `json:"sessionId" yaml:"sessionId"`
	ParentSessionID string `json:"parentSessionId,omitempty" yaml:"parentSessionId,omitempty"`
	SchemaVersion   int    `json:"schemaVersion" yaml:"schemaVersion"`
}

type Model struct {
	Provider       string `json:"provider" yaml:"provider"`
	Model          string `json:"model" yaml:"model"`
	HarnessVersion string `json:"harnessVersion,omitempty" yaml:"harnessVersion,omitempty"`
}

type Timestamp struct {
	Start    int64 `json:"start" yaml:"start"`
	End      int64 `json:"end" yaml:"end"`
	Ingested any   `json:"ingested,omitempty" yaml:"ingested,omitempty"`
}

type Source struct {
	FilePath string `json:"filePath" yaml:"filePath"`
	Format   string `json:"format" yaml:"format"`
}

type Git struct {
	Branch   string `json:"branch" yaml:"branch"`
	Remote   string `json:"remote" yaml:"remote"`
	Worktree string `json:"worktree" yaml:"worktree"`
}

type Project struct {
	Hash     string `json:"hash" yaml:"hash"`
	FilePath string `json:"filePath" yaml:"filePath"`
	Name     string `json:"name" yaml:"name"`
}

type Stats struct {
	TurnCount     int `json:"turnCount" yaml:"turnCount"`
	ToolCallCount int `json:"toolCallCount,omitempty" yaml:"toolCallCount,omitempty"`
	SubagentCount int `json:"subagentCount,omitempty" yaml:"subagentCount,omitempty"`
	DurationMs    int `json:"durationMs,omitempty" yaml:"durationMs,omitempty"`
	TokensIn      int `json:"tokensIn,omitempty" yaml:"tokensIn,omitempty"`
	TokensOut     int `json:"tokensOut,omitempty" yaml:"tokensOut,omitempty"`
}

type Quality struct {
	TitleGenerated          any    `json:"titleGenerated,omitempty" yaml:"titleGenerated,omitempty"`
	Outcome                 string `json:"outcome,omitempty" yaml:"outcome,omitempty"`
	FilesTouched            any    `json:"filesTouched,omitempty" yaml:"filesTouched,omitempty"`
	LinesChanged            any    `json:"linesChanged,omitempty" yaml:"linesChanged,omitempty"`
	RetryLoops              any    `json:"retryLoops,omitempty" yaml:"retryLoops,omitempty"`
	RetryTokensWasted       any    `json:"retryTokensWasted,omitempty" yaml:"retryTokensWasted,omitempty"`
	WithinSessionReverts    any    `json:"withinSessionReverts,omitempty" yaml:"withinSessionReverts,omitempty"`
	SignalDensity           any    `json:"signalDensity,omitempty" yaml:"signalDensity,omitempty"`
	SpecQualityScore        any    `json:"specQualityScore,omitempty" yaml:"specQualityScore,omitempty"`
	ExplorationRatio        any    `json:"explorationRatio,omitempty" yaml:"explorationRatio,omitempty"`
	ScopeBreadth            any    `json:"scopeBreadth,omitempty" yaml:"scopeBreadth,omitempty"`
	DiscoveryTurns          any    `json:"discoveryTurns,omitempty" yaml:"discoveryTurns,omitempty"`
	M2TokenOutcomeRatio     any    `json:"m2TokenOutcomeRatio,omitempty" yaml:"m2TokenOutcomeRatio,omitempty"`
	M3UniqueToolCount       any    `json:"m3UniqueToolCount,omitempty" yaml:"m3UniqueToolCount,omitempty"`
	M4ErrorRecoveryCount    any    `json:"m4ErrorRecoveryCount,omitempty" yaml:"m4ErrorRecoveryCount,omitempty"`
	M4ConsecutiveErrorMax   any    `json:"m4ConsecutiveErrorMax,omitempty" yaml:"m4ConsecutiveErrorMax,omitempty"`
	M5ContextUtilizationPct any    `json:"m5ContextUtilizationPct,omitempty" yaml:"m5ContextUtilizationPct,omitempty"`
	M5PeakContextTokens     any    `json:"m5PeakContextTokens,omitempty" yaml:"m5PeakContextTokens,omitempty"`
	M5AvgMessageTokens      any    `json:"m5AvgMessageTokens,omitempty" yaml:"m5AvgMessageTokens,omitempty"`
	M6OutputSurvivalPct     any    `json:"m6OutputSurvivalPct,omitempty" yaml:"m6OutputSurvivalPct,omitempty"`
	M6LinesSurvived         any    `json:"m6LinesSurvived,omitempty" yaml:"m6LinesSurvived,omitempty"`
	M6LinesTotal            any    `json:"m6LinesTotal,omitempty" yaml:"m6LinesTotal,omitempty"`
	M7SpecWordCount         any    `json:"m7SpecWordCount,omitempty" yaml:"m7SpecWordCount,omitempty"`
	M7SpecHasExamples       any    `json:"m7SpecHasExamples,omitempty" yaml:"m7SpecHasExamples,omitempty"`
	M7SpecHasConstraints    any    `json:"m7SpecHasConstraints,omitempty" yaml:"m7SpecHasConstraints,omitempty"`
	ComputedAt              any    `json:"computedAt,omitempty" yaml:"computedAt,omitempty"`
	ComputeVersion          any    `json:"computeVersion,omitempty" yaml:"computeVersion,omitempty"`
}

type Subagent struct {
	SessionID  string `json:"sessionId" yaml:"sessionId"`
	ParentUUID string `json:"parentUuid" yaml:"parentUuid"`
}

type Diagnostics struct {
	Partial bool `json:"partial,omitempty" yaml:"partial,omitempty"`
}

type ValidPayload struct {
	Name        string       `json:"name" yaml:"name"`
	Description string       `json:"description" yaml:"description"`
	Identity    Identity     `json:"identity" yaml:"identity"`
	Model       Model        `json:"model" yaml:"model"`
	Timestamp   Timestamp    `json:"timestamp" yaml:"timestamp"`
	Source      Source       `json:"source" yaml:"source"`
	Git         *Git         `json:"git,omitempty" yaml:"git,omitempty"`
	Project     *Project     `json:"project,omitempty" yaml:"project,omitempty"`
	Stats       Stats        `json:"stats" yaml:"stats"`
	Quality     *Quality     `json:"quality,omitempty" yaml:"quality,omitempty"`
	Subagents   []Subagent   `json:"subagents,omitempty" yaml:"subagents,omitempty"`
	Diagnostics *Diagnostics `json:"diagnostics,omitempty" yaml:"diagnostics,omitempty"`
}

type InvalidPayload struct {
	Name          string     `json:"name" yaml:"name"`
	Description   string     `json:"description" yaml:"description"`
	ExpectedError string     `json:"expectedError" yaml:"expectedError"`
	Identity      *Identity  `json:"identity,omitempty" yaml:"identity,omitempty"`
	Model         *Model     `json:"model,omitempty" yaml:"model,omitempty"`
	Timestamp     *Timestamp `json:"timestamp,omitempty" yaml:"timestamp,omitempty"`
	Source        *Source    `json:"source,omitempty" yaml:"source,omitempty"`
	Stats         *Stats     `json:"stats,omitempty" yaml:"stats,omitempty"`
	Raw           string     `json:"_raw,omitempty" yaml:"_raw,omitempty"`
}

type Tuple2[T any, U any] struct {
	First  T
	Second U
}

type Tuple3[T any, U any, V any] struct {
	First  T
	Second U
	Third  V
}

func CartesianProduct[T any](slices ...[]T) [][]T {
	if len(slices) == 0 {
		return nil
	}

	result := make([][]T, 0)
	indices := make([]int, len(slices))

	for {
		combination := make([]T, len(slices))
		for i := range slices {
			combination[i] = slices[i][indices[i]]
		}
		result = append(result, combination)

		breakLoop := true
		for j := len(slices) - 1; j >= 0; j-- {
			if indices[j] < len(slices[j])-1 {
				indices[j]++
				for k := j + 1; k < len(slices); k++ {
					indices[k] = 0
				}
				breakLoop = false
				break
			}
		}
		if breakLoop {
			break
		}
	}

	return result
}

func CartesianProduct2[T any, U any](a []T, b []U) []Tuple2[T, U] {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}

	result := make([]Tuple2[T, U], 0, len(a)*len(b))
	for _, av := range a {
		for _, bv := range b {
			result = append(result, Tuple2[T, U]{First: av, Second: bv})
		}
	}
	return result
}

func CartesianProduct3[T any, U any, V any](a []T, b []U, c []V) []Tuple3[T, U, V] {
	if len(a) == 0 || len(b) == 0 || len(c) == 0 {
		return nil
	}

	result := make([]Tuple3[T, U, V], 0, len(a)*len(b)*len(c))
	for _, av := range a {
		for _, bv := range b {
			for _, cv := range c {
				result = append(result, Tuple3[T, U, V]{First: av, Second: bv, Third: cv})
			}
		}
	}
	return result
}

type CombinatorialTestCase struct {
	Name     string
	Fixtures []FixtureItem
}

func GenerateCombinatorialTestCases(categoryNames []string, fixturesByCategory map[string][]FixtureItem) []CombinatorialTestCase {
	if len(categoryNames) == 0 {
		return nil
	}

	slices := make([][]FixtureItem, len(categoryNames))
	for i, name := range categoryNames {
		slices[i] = fixturesByCategory[name]
	}

	combos := CartesianProduct(slices...)

	result := make([]CombinatorialTestCase, 0, len(combos))
	for _, combo := range combos {
		name := ""
		for j, item := range combo {
			if j > 0 {
				name += "_"
			}
			name += item.Name
		}
		result = append(result, CombinatorialTestCase{
			Name:     name,
			Fixtures: combo,
		})
	}

	return result
}

func LoadAdversarialFixtures(path string) (*AdversarialFixtures, error) {
	var fixtures AdversarialFixtures
	if err := decodeFixtureFile(path, &fixtures); err != nil {
		return nil, err
	}
	return &fixtures, nil
}

func LoadQualityFixtures(path string) (*QualityFixtures, error) {
	var fixtures QualityFixtures
	if err := decodeFixtureFile(path, &fixtures); err != nil {
		return nil, err
	}
	return &fixtures, nil
}

func LoadStatsFixtures(path string) (*StatsFixtures, error) {
	var fixtures StatsFixtures
	if err := decodeFixtureFile(path, &fixtures); err != nil {
		return nil, err
	}
	return &fixtures, nil
}

func LoadModelFixtures(path string) (*ModelFixtures, error) {
	var fixtures ModelFixtures
	if err := decodeFixtureFile(path, &fixtures); err != nil {
		return nil, err
	}
	return &fixtures, nil
}

func LoadTimestampFixtures(path string) (*TimestampFixtures, error) {
	var fixtures TimestampFixtures
	if err := decodeFixtureFile(path, &fixtures); err != nil {
		return nil, err
	}
	return &fixtures, nil
}

func LoadSessionFixtures(path string) (*SessionFixtures, error) {
	var fixtures SessionFixtures
	if err := decodeFixtureFile(path, &fixtures); err != nil {
		return nil, err
	}
	return &fixtures, nil
}

func LoadValidPayloads(path string) ([]ValidPayload, error) {
	var payloads []ValidPayload
	if err := decodeFixtureFile(path, &payloads); err != nil {
		return nil, err
	}
	return payloads, nil
}

func LoadInvalidPayloads(path string) ([]InvalidPayload, error) {
	var payloads []InvalidPayload
	if err := decodeFixtureFile(path, &payloads); err != nil {
		return nil, err
	}
	return payloads, nil
}

func decodeFixtureFile(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read fixture %s: %w", path, err)
	}
	return decodeFixtureData(data, path, target)
}

func decodeFixtureData(data []byte, source string, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode fixture %s with known fields: %w", source, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("fixture %s must contain exactly one YAML document", source)
		}
		return fmt.Errorf("decode trailing fixture document %s: %w", source, err)
	}
	return nil
}

type Tuple8[T, U, V, W, X, Y, Z, A any] struct {
	First   T
	Second  U
	Third   V
	Fourth  W
	Fifth   X
	Sixth   Y
	Seventh Z
	Eighth  A
}

func CartesianProduct8[T, U, V, W, X, Y, Z, A any](a []T, b []U, c []V, d []W, e []X, f []Y, g []Z, h []A) []Tuple8[T, U, V, W, X, Y, Z, A] {
	result := make([]Tuple8[T, U, V, W, X, Y, Z, A], 0, len(a)*len(b)*len(c)*len(d)*len(e)*len(f)*len(g)*len(h))
	for _, av := range a {
		for _, bv := range b {
			for _, cv := range c {
				for _, dv := range d {
					for _, ev := range e {
						for _, fv := range f {
							for _, gv := range g {
								for _, hv := range h {
									result = append(result, Tuple8[T, U, V, W, X, Y, Z, A]{First: av, Second: bv, Third: cv, Fourth: dv, Fifth: ev, Sixth: fv, Seventh: gv, Eighth: hv})
								}
							}
						}
					}
				}
			}
		}
	}
	return result
}
