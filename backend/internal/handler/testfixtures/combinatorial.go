package testfixtures

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// GenerateCombinatorialPayloads generates combinations of valid payloads
func GenerateCombinatorialPayloads(basePath string) ([]ValidPayload, error) {
	sessions, err := LoadSessionFixtures(filepath.Join(basePath, "fixtures/sessions.yaml"))
	if err != nil {
		return nil, err
	}
	timestamps, err := LoadTimestampFixtures(filepath.Join(basePath, "fixtures/timestamps.yaml"))
	if err != nil {
		return nil, err
	}
	models, err := LoadModelFixtures(filepath.Join(basePath, "fixtures/models.yaml"))
	if err != nil {
		return nil, err
	}
	stats, err := LoadStatsFixtures(filepath.Join(basePath, "fixtures/stats.yaml"))
	if err != nil {
		return nil, err
	}
	quality, err := LoadQualityFixtures(filepath.Join(basePath, "fixtures/quality.yaml"))
	if err != nil {
		return nil, err
	}

	var payloads []ValidPayload

	// Cross domains: SessionIDs × Timestamps × Providers × Models × Stats(TurnCounts) × Quality(Outcomes) × Sources
	validSessionIDs := filterFixtureItems(sessions.SessionIDs)
	validTimestamps := filterTimestamps(timestamps.Timestamps)
	validProviders := filterFixtureItems(models.Providers)
	validModels := filterFixtureItems(models.Models)
	validTurnCounts := filterFixtureItems(stats.TurnCounts)
	validOutcomes := filterFixtureItems(quality.Outcomes)
	validLinesChanged := filterFixtureItems(quality.LinesChanged)

	validSources := []Source{
		{FilePath: "/path/to/transcript.jsonl", Format: "jsonl"},
		{FilePath: "/path/to/transcript.json", Format: "json"},
	}

	combos := CartesianProduct8(validSessionIDs, validTimestamps, validProviders, validModels, validTurnCounts, validOutcomes, validLinesChanged, validSources)

	for _, combo := range combos {
		p := createBasePayload()
		p.Name = fmt.Sprintf("cross_%s_%s_%s_%s_%s_%s_%s_%s",
			combo.First.Name, combo.Second.Name, combo.Third.Name,
			combo.Fourth.Name, combo.Fifth.Name, combo.Sixth.Name, combo.Seventh.Name, combo.Eighth.Format)
		p.Description = "Combinatorial cross-domain test"

		// Identity
		p.Identity.SessionID = combo.First.Value.(string)

		// Timestamp
		p.Timestamp.Start = combo.Second.Start
		p.Timestamp.End = combo.Second.End

		// Model
		p.Model.Provider = combo.Third.Value.(string)
		p.Model.Model = combo.Fourth.Value.(string)

		// Stats
		p.Stats.TurnCount = toInt(combo.Fifth.Value)

		// Quality
		q := *p.Quality // clone base quality
		if combo.Sixth.Value != nil {
			q.Outcome = combo.Sixth.Value.(string)
		}
		if combo.Seventh.Value != nil {
			q.LinesChanged = combo.Seventh.Value
		}
		p.Quality = &q

		// Source
		p.Source = combo.Eighth

		// Optional fields
		p.Git = &Git{
			Branch:   "main",
			Remote:   "origin",
			Worktree: "/app",
		}
		p.Project = &Project{
			Hash:     "abc1234",
			FilePath: "/app/project.json",
			Name:     "test-project",
		}
		p.Diagnostics = &Diagnostics{
			Partial: false,
		}
		p.Subagents = []Subagent{
			{SessionID: "child-session", ParentUUID: p.Identity.SessionID},
		}

		payloads = append(payloads, p)
	}

	return payloads, nil
}

// GenerateInvalidPayloads generates invalid payloads by injecting adversarial data into base payloads
func GenerateInvalidPayloads(basePath string) ([]InvalidPayload, error) {
	adversarial, err := LoadAdversarialFixtures(filepath.Join(basePath, "fixtures/adversarial.yaml"))
	if err != nil {
		return nil, err
	}
	timestamps, err := LoadTimestampFixtures(filepath.Join(basePath, "fixtures/timestamps.yaml"))
	if err != nil {
		return nil, err
	}

	var payloads []InvalidPayload

	// Session ID injections
	for _, inj := range adversarial.InjectionPayloads {
		p := createBaseInvalidPayload()
		p.Name = fmt.Sprintf("invalid_session_%s", inj.Name)
		p.Description = "Invalid session ID: " + inj.Description
		p.ExpectedError = "is not valid 'uuid'"
		p.Identity.SessionID = inj.Payload
		payloads = append(payloads, p)
	}

	// Model Provider injections
	for _, inj := range adversarial.InjectionPayloads {
		p := createBaseInvalidPayload()
		p.Name = fmt.Sprintf("invalid_model_%s", inj.Name)
		p.Description = "Invalid model provider: " + inj.Description
		p.ExpectedError = "injection payload"
		p.Model.Provider = inj.Payload
		payloads = append(payloads, p)
	}

	// Negative timestamps
	for _, ts := range timestamps.Timestamps {
		if strings.Contains(ts.Name, "negative") {
			p := createBaseInvalidPayload()
			p.Name = fmt.Sprintf("invalid_timestamp_%s", ts.Name)
			p.Description = "Invalid negative timestamp: " + ts.Description
			p.ExpectedError = "must be >= 0"
			p.Timestamp.Start = ts.Start
			p.Timestamp.End = ts.End
			payloads = append(payloads, p)
		}
	}

	// Malformed JSON (using Raw field)
	for _, jsonPayload := range adversarial.JSONPayloads {
		p := createBaseInvalidPayload()
		p.Name = fmt.Sprintf("invalid_json_%s", jsonPayload.Name)
		p.Description = "Malformed JSON: " + jsonPayload.Description
		p.ExpectedError = "invalid character"
		p.Raw = jsonPayload.Value
		payloads = append(payloads, p)
	}

	// Invalid source.format values
	for _, format := range []string{"xml", "pdf", "docx"} {
		p := createBaseInvalidPayload()
		p.Name = fmt.Sprintf("invalid_source_format_%s", format)
		p.Description = "Invalid source format: " + format
		p.ExpectedError = "value must be one of"
		p.Source.Format = format
		payloads = append(payloads, p)
	}

	// Missing required fields
	p1 := createBaseInvalidPayload()
	p1.Name = "missing_identity"
	p1.Description = "Missing required identity field"
	p1.ExpectedError = "missing properties: 'identity'"
	p1.Identity = nil
	payloads = append(payloads, p1)

	p2 := createBaseInvalidPayload()
	p2.Name = "missing_model"
	p2.Description = "Missing required model field"
	p2.ExpectedError = "missing properties: 'model'"
	p2.Model = nil
	payloads = append(payloads, p2)

	p3 := createBaseInvalidPayload()
	p3.Name = "missing_timestamp"
	p3.Description = "Missing required timestamp field"
	p3.ExpectedError = "missing properties: 'timestamp'"
	p3.Timestamp = nil
	payloads = append(payloads, p3)

	p4 := createBaseInvalidPayload()
	p4.Name = "missing_source"
	p4.Description = "Missing required source field"
	p4.ExpectedError = "missing properties: 'source'"
	p4.Source = nil
	payloads = append(payloads, p4)

	// Helper to generate payloads with missing sub-fields
	generateMissingSubfield := func(name, desc, expectedError string, deleteFunc func(map[string]interface{})) InvalidPayload {
		base := createBasePayload()
		b, _ := json.Marshal(base)
		var m map[string]interface{}
		json.Unmarshal(b, &m)
		deleteFunc(m)

		// Remove fields from m that are not part of the payload, though base is ValidPayload
		// and json.Marshal will marshal everything into it.
		// Actually, ValidPayload has no name, description, expectedError fields since it's the actual payload representation.
		// Wait, ValidPayload struct has Name and Description! Let's delete them.
		delete(m, "name")
		delete(m, "description")

		rawBytes, _ := json.Marshal(m)

		p := createBaseInvalidPayload()
		p.Name = name
		p.Description = desc
		p.ExpectedError = expectedError
		p.Raw = string(rawBytes)
		return p
	}

	payloads = append(payloads, generateMissingSubfield(
		"missing_identity_sessionId",
		"Missing required identity.sessionId",
		"missing properties: 'sessionId'",
		func(m map[string]interface{}) {
			if id, ok := m["identity"].(map[string]interface{}); ok {
				delete(id, "sessionId")
			}
		},
	))

	payloads = append(payloads, generateMissingSubfield(
		"missing_timestamp_start",
		"Missing required timestamp.start",
		"missing properties: 'start'",
		func(m map[string]interface{}) {
			if ts, ok := m["timestamp"].(map[string]interface{}); ok {
				delete(ts, "start")
			}
		},
	))

	payloads = append(payloads, generateMissingSubfield(
		"missing_timestamp_end",
		"Missing required timestamp.end",
		"missing properties: 'end'",
		func(m map[string]interface{}) {
			if ts, ok := m["timestamp"].(map[string]interface{}); ok {
				delete(ts, "end")
			}
		},
	))

	payloads = append(payloads, generateMissingSubfield(
		"missing_source_filePath",
		"Missing required source.filePath",
		"missing properties: 'filePath'",
		func(m map[string]interface{}) {
			if src, ok := m["source"].(map[string]interface{}); ok {
				delete(src, "filePath")
			}
		},
	))

	payloads = append(payloads, generateMissingSubfield(
		"missing_source_format",
		"Missing required source.format",
		"missing properties: 'format'",
		func(m map[string]interface{}) {
			if src, ok := m["source"].(map[string]interface{}); ok {
				delete(src, "format")
			}
		},
	))

	payloads = append(payloads, generateMissingSubfield(
		"missing_model_provider",
		"Missing required model.provider",
		"missing properties: 'provider'",
		func(m map[string]interface{}) {
			if mod, ok := m["model"].(map[string]interface{}); ok {
				delete(mod, "provider")
			}
		},
	))

	payloads = append(payloads, generateMissingSubfield(
		"missing_model_model",
		"Missing required model.model",
		"missing properties: 'model'",
		func(m map[string]interface{}) {
			if mod, ok := m["model"].(map[string]interface{}); ok {
				delete(mod, "model")
			}
		},
	))

	return payloads, nil
}

// Helper Functions

func filterFixtureItems(items []FixtureItem) []FixtureItem {
	var valid []FixtureItem
	for _, item := range items {
		if isNameValid(item.Name) {
			valid = append(valid, item)
		}
	}
	return valid
}

func filterTimestamps(items []TimestampRange) []TimestampRange {
	var valid []TimestampRange
	for _, item := range items {
		if isNameValid(item.Name) {
			valid = append(valid, item)
		}
	}
	return valid
}

func isNameValid(name string) bool {
	lower := strings.ToLower(name)
	invalidWords := []string{"invalid", "too_short", "too_long", "empty", "overflow", "negative", "unknown", "timeout", "partial", "start_after_end", "before_start", "zero_uuid"}
	for _, w := range invalidWords {
		if strings.Contains(lower, w) {
			return false
		}
	}
	return true
}

func toInt(v interface{}) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	}
	return 0
}

func createBasePayload() ValidPayload {
	return ValidPayload{
		Identity: Identity{
			SessionID:     "550e8400-e29b-41d4-a716-446655440000",
			SchemaVersion: 2,
		},
		Model: Model{
			Provider: "openai",
			Model:    "gpt-4",
		},
		Timestamp: Timestamp{
			Start: 1700000000000,
			End:   1700000060000,
		},
		Source: Source{
			FilePath: "/path/to/transcript.jsonl",
			Format:   "jsonl",
		},
		Stats: Stats{
			TurnCount:     10,
			ToolCallCount: 5,
			SubagentCount: 2,
			DurationMs:    60000,
			TokensIn:      1000,
			TokensOut:     500,
		},
		Quality: &Quality{
			Outcome:                 "success",
			FilesTouched:            3,
			LinesChanged:            50,
			RetryLoops:              1,
			RetryTokensWasted:       100,
			WithinSessionReverts:    0,
			SignalDensity:           0.85,
			SpecQualityScore:        0.9,
			ExplorationRatio:        0.3,
			ScopeBreadth:            5,
			DiscoveryTurns:          2,
			M2TokenOutcomeRatio:     0.7,
			M3UniqueToolCount:       4,
			M4ErrorRecoveryCount:    1,
			M4ConsecutiveErrorMax:   2,
			M5ContextUtilizationPct: 0.45,
			M5PeakContextTokens:     4000,
			M5AvgMessageTokens:      400,
			M6OutputSurvivalPct:     0.95,
			M6LinesSurvived:         48,
			M6LinesTotal:            50,
			M7SpecWordCount:         150,
			M7SpecHasExamples:       true,
			M7SpecHasConstraints:    true,
			ComputedAt:              1700000060000,
			ComputeVersion:          1,
		},
	}
}

func createBaseInvalidPayload() InvalidPayload {
	return InvalidPayload{
		Identity: &Identity{
			SessionID:     "550e8400-e29b-41d4-a716-446655440000",
			SchemaVersion: 2,
		},
		Model: &Model{
			Provider: "openai",
			Model:    "gpt-4",
		},
		Timestamp: &Timestamp{
			Start: 1700000000000,
			End:   1700000060000,
		},
		Source: &Source{
			FilePath: "/path/to/transcript.jsonl",
			Format:   "jsonl",
		},
		Stats: &Stats{
			TurnCount: 10,
		},
	}
}
