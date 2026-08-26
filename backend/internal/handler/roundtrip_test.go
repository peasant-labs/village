package handler

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/handler/testfixtures"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
)

func TestRoundTrip(t *testing.T) {
	// 1. Load fixtures from valid payloads
	validPayloads, err := testfixtures.LoadValidPayloads("testfixtures/testdata/valid/payloads.yaml")
	if err != nil {
		t.Fatalf("failed to load valid payloads: %v", err)
	}

	// 2. Generate combinatorial payloads
	combinatorialPayloads, err := testfixtures.GenerateCombinatorialPayloads("testfixtures/testdata")
	if err != nil {
		t.Fatalf("failed to generate combinatorial payloads: %v", err)
	}

	allPayloads := append(validPayloads, combinatorialPayloads...)

	for _, p := range allPayloads {
		t.Run(p.Name, func(t *testing.T) {
			req := payloadToSchema(p)

			// Round-trip
			blobKey := "test-blob-key"
			blobSize := int64(1234)
			schemaVersion := fmt.Sprintf("%d", req.Identity.SchemaVersion)

			params := schemaToTranscriptParams(req, blobKey, blobSize, schemaVersion, sessionorigin.Unknown)
			params.LocalID = string(req.Identity.SessionID)

			transcript := paramsToTranscript(params)

			result := transcriptToSchema(transcript)

			// Assertions
			assertPublishRequestEqual(t, req, result)
		})
	}
}

// TestRoundTrip_License pins the legal axis through the full
// param↔transcript↔schema round-trip: a producer-stamped license survives
// schemaToTranscriptParams (store) and transcriptToSchema (reverse), and an unset
// license stays empty (NULL ⇄ ""). The pull emit path is covered separately by
// TestPull_LicenseRoundTrip_RealPostgres (real DB, both pull mappers).
func TestRoundTrip_License(t *testing.T) {
	// Derive cases from the published contract menu so every newly pinned license
	// is covered without a second hand-maintained list.
	cases := append([]schema.License{""}, schema.AllLicenses...)
	for _, lic := range cases {
		name := string(lic)
		if name == "" {
			name = "none"
		}
		t.Run(name, func(t *testing.T) {
			req := schema.PublishRequest{
				License:  lic,
				Identity: schema.SessionIdentity{SessionID: "550e8400-e29b-41d4-a716-446655440000", SchemaVersion: 2},
				Model:    schema.ModelInfo{Harness: "claude-code", Model: "m"},
			}
			params := schemaToTranscriptParams(req, "blob", 1, "2", sessionorigin.Unknown)
			params.LocalID = string(req.Identity.SessionID)

			// store-side: empty ⇒ NULL; a value ⇒ set (FK-safe — the menu is already
			// enforced at the publish 422 gate, so the mapper does not re-validate).
			if lic == "" && params.LicenseID.Valid {
				t.Errorf("empty license should map to NULL, got %q", params.LicenseID.String)
			}
			if lic != "" && (!params.LicenseID.Valid || params.LicenseID.String != string(lic)) {
				t.Errorf("license %q should map to a set license_id, got valid=%v %q", lic, params.LicenseID.Valid, params.LicenseID.String)
			}

			// reverse: transcript ⇒ schema preserves the license verbatim.
			got := transcriptToSchema(paramsToTranscript(params))
			if got.License != lic {
				t.Errorf("round-trip License: got %q, want %q", got.License, lic)
			}
		})
	}
}

func paramsToTranscript(p sqlc.CreateTranscriptParams) sqlc.Transcript {
	return sqlc.Transcript{
		LicenseID:               p.LicenseID,
		LocalID:                 p.LocalID,
		Title:                   p.Title,
		Description:             p.Description,
		Visibility:              p.Visibility,
		ModelProvider:           p.ModelProvider,
		ModelName:               p.ModelName,
		HarnessVersion:          p.HarnessVersion,
		SessionStart:            p.SessionStart,
		SessionEnd:              p.SessionEnd,
		TurnCount:               p.TurnCount,
		TokenCount:              p.TokenCount,
		BlobKey:                 p.BlobKey,
		BlobSizeBytes:           p.BlobSizeBytes,
		SchemaVersion:           p.SchemaVersion,
		ParentSessionID:         p.ParentSessionID,
		IngestedAt:              p.IngestedAt,
		SourceFilePath:          p.SourceFilePath,
		SourceFormat:            p.SourceFormat,
		GitBranch:               p.GitBranch,
		GitRemote:               p.GitRemote,
		GitWorktree:             p.GitWorktree,
		ProjectHash:             p.ProjectHash,
		ProjectPath:             p.ProjectPath,
		ProjectName:             p.ProjectName,
		ToolCallCount:           p.ToolCallCount,
		SubagentCount:           p.SubagentCount,
		DurationMs:              p.DurationMs,
		Subagents:               p.Subagents,
		DiagnosticsWarnings:     p.DiagnosticsWarnings,
		DiagnosticsPartial:      p.DiagnosticsPartial,
		TokensIn:                p.TokensIn,
		TokensOut:               p.TokensOut,
		TitleGenerated:          p.TitleGenerated,
		Outcome:                 p.Outcome,
		FilesTouched:            p.FilesTouched,
		LinesChanged:            p.LinesChanged,
		RetryLoops:              p.RetryLoops,
		RetryTokensWasted:       p.RetryTokensWasted,
		WithinSessionReverts:    p.WithinSessionReverts,
		SignalDensity:           p.SignalDensity,
		SpecQualityScore:        p.SpecQualityScore,
		ExplorationRatio:        p.ExplorationRatio,
		ScopeBreadth:            p.ScopeBreadth,
		DiscoveryTurns:          p.DiscoveryTurns,
		M2TokenOutcomeRatio:     p.M2TokenOutcomeRatio,
		M3UniqueToolCount:       p.M3UniqueToolCount,
		M4ErrorRecoveryCount:    p.M4ErrorRecoveryCount,
		M4ConsecutiveErrorMax:   p.M4ConsecutiveErrorMax,
		M5ContextUtilizationPct: p.M5ContextUtilizationPct,
		M5PeakContextTokens:     p.M5PeakContextTokens,
		M5AvgMessageTokens:      p.M5AvgMessageTokens,
		M6OutputSurvivalPct:     p.M6OutputSurvivalPct,
		M6LinesSurvived:         p.M6LinesSurvived,
		M6LinesTotal:            p.M6LinesTotal,
		M7SpecWordCount:         p.M7SpecWordCount,
		M7SpecHasExamples:       p.M7SpecHasExamples,
		M7SpecHasConstraints:    p.M7SpecHasConstraints,
		ComputedAt:              p.ComputedAt,
		ComputeVersion:          p.ComputeVersion,
	}
}

func payloadToSchema(p testfixtures.ValidPayload) schema.PublishRequest {
	req := schema.PublishRequest{
		Identity: schema.SessionIdentity{
			SessionID:     schema.SessionID(p.Identity.SessionID),
			SchemaVersion: p.Identity.SchemaVersion,
		},
		Model: schema.ModelInfo{
			Harness:        schema.Harness(p.Model.Provider),
			Model:          schema.ModelID(p.Model.Model),
			HarnessVersion: p.Model.HarnessVersion,
		},
		Timestamp: schema.TimestampInfo{
			Start: p.Timestamp.Start,
			End:   p.Timestamp.End,
		},
		Source: schema.SourceInfo{
			FilePath: p.Source.FilePath,
			Format:   schema.SourceFormat(p.Source.Format),
		},
		Stats: schema.SessionStats{
			TurnCount:     p.Stats.TurnCount,
			ToolCallCount: p.Stats.ToolCallCount,
			SubagentCount: p.Stats.SubagentCount,
			DurationMs:    int64(p.Stats.DurationMs),
			TokensIn:      p.Stats.TokensIn,
			TokensOut:     p.Stats.TokensOut,
		},
	}

	if p.Identity.ParentSessionID != "" {
		sid := schema.SessionID(p.Identity.ParentSessionID)
		req.Identity.ParentSessionID = &sid
	}

	if p.Timestamp.Ingested != nil {
		req.Timestamp.Ingested = anyToInt64Ptr(p.Timestamp.Ingested)
	}

	if p.Git != nil {
		req.Git = schema.GitContext{
			Branch:   &p.Git.Branch,
			Remote:   &p.Git.Remote,
			Worktree: &p.Git.Worktree,
		}
	}

	if p.Project != nil {
		req.Project = schema.ProjectContext{
			Hash:     schema.ProjectHash(p.Project.Hash),
			FilePath: p.Project.FilePath,
			Name:     p.Project.Name,
		}
	}

	if p.Quality != nil {
		req.Quality = &schema.QualityMetrics{
			Outcome:                 toOutcomePtr(p.Quality.Outcome),
			FilesTouched:            anyToIntPtr(p.Quality.FilesTouched),
			LinesChanged:            anyToIntPtr(p.Quality.LinesChanged),
			RetryLoops:              anyToIntPtr(p.Quality.RetryLoops),
			RetryTokensWasted:       anyToIntPtr(p.Quality.RetryTokensWasted),
			WithinSessionReverts:    anyToIntPtr(p.Quality.WithinSessionReverts),
			SignalDensity:           anyToFloat64Ptr(p.Quality.SignalDensity),
			SpecQualityScore:        anyToFloat64Ptr(p.Quality.SpecQualityScore),
			ExplorationRatio:        anyToFloat64Ptr(p.Quality.ExplorationRatio),
			ScopeBreadth:            anyToIntPtr(p.Quality.ScopeBreadth),
			DiscoveryTurns:          anyToIntPtr(p.Quality.DiscoveryTurns),
			M2TokenOutcomeRatio:     anyToFloat64Ptr(p.Quality.M2TokenOutcomeRatio),
			M3UniqueToolCount:       anyToIntPtr(p.Quality.M3UniqueToolCount),
			M4ErrorRecoveryCount:    anyToIntPtr(p.Quality.M4ErrorRecoveryCount),
			M4ConsecutiveErrorMax:   anyToIntPtr(p.Quality.M4ConsecutiveErrorMax),
			M5ContextUtilizationPct: anyToFloat64Ptr(p.Quality.M5ContextUtilizationPct),
			M5PeakContextTokens:     anyToIntPtr(p.Quality.M5PeakContextTokens),
			M5AvgMessageTokens:      anyToIntPtr(p.Quality.M5AvgMessageTokens),
			M6OutputSurvivalPct:     anyToFloat64Ptr(p.Quality.M6OutputSurvivalPct),
			M6LinesSurvived:         anyToIntPtr(p.Quality.M6LinesSurvived),
			M6LinesTotal:            anyToIntPtr(p.Quality.M6LinesTotal),
			M7SpecWordCount:         anyToIntPtr(p.Quality.M7SpecWordCount),
			M7SpecHasExamples:       anyToBoolPtr(p.Quality.M7SpecHasExamples),
			M7SpecHasConstraints:    anyToBoolPtr(p.Quality.M7SpecHasConstraints),
			ComputedAt:              anyToInt64Ptr(p.Quality.ComputedAt),
			ComputeVersion:          anyToIntPtr(p.Quality.ComputeVersion),
		}
		if p.Quality.TitleGenerated != nil {
			if s, ok := p.Quality.TitleGenerated.(string); ok && s != "" {
				req.Quality.TitleGenerated = &s
			}
		}
	}

	if len(p.Subagents) > 0 {
		req.Subagents = make([]schema.SubagentRef, len(p.Subagents))
		for i, s := range p.Subagents {
			req.Subagents[i] = schema.SubagentRef{
				SessionID:  schema.SessionID(s.SessionID),
				ParentUUID: schema.SessionID(s.ParentUUID),
			}
		}
	}

	if p.Diagnostics != nil {
		req.Diagnostics.Partial = &p.Diagnostics.Partial
	}

	return req
}

func toOutcomePtr(s string) *schema.SessionOutcome {
	if s == "" {
		return nil
	}
	o := schema.SessionOutcome(s)
	return &o
}

func anyToIntPtr(v any) *int {
	if v == nil {
		return nil
	}
	var i int
	switch val := v.(type) {
	case int:
		i = val
	case int64:
		i = int(val)
	case float64:
		i = int(val)
	default:
		return nil
	}
	return &i
}

func anyToInt64Ptr(v any) *int64 {
	if v == nil {
		return nil
	}
	var i int64
	switch val := v.(type) {
	case int:
		i = int64(val)
	case int64:
		i = val
	case float64:
		i = int64(val)
	default:
		return nil
	}
	return &i
}

func anyToFloat64Ptr(v any) *float64 {
	if v == nil {
		return nil
	}
	var f float64
	switch val := v.(type) {
	case float64:
		f = val
	case int:
		f = float64(val)
	case int64:
		f = float64(val)
	default:
		return nil
	}
	return &f
}

func anyToBoolPtr(v any) *bool {
	if v == nil {
		return nil
	}
	if b, ok := v.(bool); ok {
		return &b
	}
	return nil
}

func assertPublishRequestEqual(t *testing.T, expected, actual schema.PublishRequest) {
	t.Helper()

	// Compare License (legal axis; empty ⇒ legacy/un-set)
	if expected.License != actual.License {
		t.Errorf("License: got %v, want %v", actual.License, expected.License)
	}

	// Compare Identity
	if expected.Identity.SessionID != actual.Identity.SessionID {
		t.Errorf("Identity.SessionID: got %v, want %v", actual.Identity.SessionID, expected.Identity.SessionID)
	}
	if !reflect.DeepEqual(expected.Identity.ParentSessionID, actual.Identity.ParentSessionID) {
		t.Errorf("Identity.ParentSessionID: got %v, want %v", actual.Identity.ParentSessionID, expected.Identity.ParentSessionID)
	}
	if expected.Identity.SchemaVersion != actual.Identity.SchemaVersion {
		t.Errorf("Identity.SchemaVersion: got %v, want %v", actual.Identity.SchemaVersion, expected.Identity.SchemaVersion)
	}

	// Compare Model
	if expected.Model.Harness != actual.Model.Harness {
		t.Errorf("Model.Harness: got %v, want %v", actual.Model.Harness, expected.Model.Harness)
	}
	if expected.Model.Model != actual.Model.Model {
		t.Errorf("Model.Model: got %v, want %v", actual.Model.Model, expected.Model.Model)
	}
	if expected.Model.HarnessVersion != actual.Model.HarnessVersion {
		t.Errorf("Model.HarnessVersion: got %v, want %v", actual.Model.HarnessVersion, expected.Model.HarnessVersion)
	}

	// Compare Timestamp
	if expected.Timestamp.Start != actual.Timestamp.Start {
		t.Errorf("Timestamp.Start: got %v, want %v", actual.Timestamp.Start, expected.Timestamp.Start)
	}
	if expected.Timestamp.End != actual.Timestamp.End {
		t.Errorf("Timestamp.End: got %v, want %v", actual.Timestamp.End, expected.Timestamp.End)
	}
	if !reflect.DeepEqual(expected.Timestamp.Ingested, actual.Timestamp.Ingested) {
		t.Errorf("Timestamp.Ingested: got %v, want %v", actual.Timestamp.Ingested, expected.Timestamp.Ingested)
	}

	// Compare Source
	if expected.Source.FilePath != actual.Source.FilePath {
		t.Errorf("Source.FilePath: got %v, want %v", actual.Source.FilePath, expected.Source.FilePath)
	}
	if expected.Source.Format != actual.Source.Format {
		t.Errorf("Source.Format: got %v, want %v", actual.Source.Format, expected.Source.Format)
	}

	// Compare Git
	if !reflect.DeepEqual(expected.Git, actual.Git) {
		t.Errorf("Git: got %+v, want %+v", actual.Git, expected.Git)
	}

	// Compare Project
	if !reflect.DeepEqual(expected.Project, actual.Project) {
		t.Errorf("Project: got %+v, want %+v", actual.Project, expected.Project)
	}

	// Compare Stats
	if expected.Stats.TurnCount != actual.Stats.TurnCount {
		t.Errorf("Stats.TurnCount: got %v, want %v", actual.Stats.TurnCount, expected.Stats.TurnCount)
	}
	if expected.Stats.ToolCallCount != actual.Stats.ToolCallCount {
		t.Errorf("Stats.ToolCallCount: got %v, want %v", actual.Stats.ToolCallCount, expected.Stats.ToolCallCount)
	}
	if expected.Stats.SubagentCount != actual.Stats.SubagentCount {
		t.Errorf("Stats.SubagentCount: got %v, want %v", actual.Stats.SubagentCount, expected.Stats.SubagentCount)
	}
	if expected.Stats.DurationMs != actual.Stats.DurationMs {
		t.Errorf("Stats.DurationMs: got %v, want %v", actual.Stats.DurationMs, expected.Stats.DurationMs)
	}
	if expected.Stats.TokensIn != actual.Stats.TokensIn {
		t.Errorf("Stats.TokensIn: got %v, want %v", actual.Stats.TokensIn, expected.Stats.TokensIn)
	}
	if expected.Stats.TokensOut != actual.Stats.TokensOut {
		t.Errorf("Stats.TokensOut: got %v, want %v", actual.Stats.TokensOut, expected.Stats.TokensOut)
	}

	// Compare Quality
	if expected.Quality == nil {
		// Round-trip always returns a non-nil QualityMetrics pointer
		// Check if all its fields are nil
		if actual.Quality != nil {
			v := reflect.ValueOf(*actual.Quality)
			for i := 0; i < v.NumField(); i++ {
				field := v.Field(i)
				if !field.IsNil() {
					t.Errorf("Quality: expected nil, but got non-nil field %s: %v", v.Type().Field(i).Name, field.Interface())
				}
			}
		}
	} else {
		if actual.Quality == nil {
			t.Error("Quality: got nil, want non-nil")
		} else {
			// Compare each field to give better error messages
			ev := reflect.ValueOf(*expected.Quality)
			av := reflect.ValueOf(*actual.Quality)
			for i := 0; i < ev.NumField(); i++ {
				ef := ev.Field(i)
				af := av.Field(i)
				fieldName := ev.Type().Field(i).Name

				if ef.IsNil() && af.IsNil() {
					continue
				}
				if ef.IsNil() || af.IsNil() {
					t.Errorf("Quality.%s: got %v, want %v", fieldName, af.Interface(), ef.Interface())
					continue
				}

				eVal := ef.Elem().Interface()
				aVal := af.Elem().Interface()

				switch evv := eVal.(type) {
				case float64:
					avv := aVal.(float64)
					if !floatsEqual(evv, avv) {
						t.Errorf("Quality.%s: got %v, want %v", fieldName, avv, evv)
					}
				default:
					if !reflect.DeepEqual(eVal, aVal) {
						t.Errorf("Quality.%s: got %v, want %v", fieldName, aVal, eVal)
					}
				}
			}
		}
	}

	// Compare Subagents
	if !reflect.DeepEqual(expected.Subagents, actual.Subagents) {
		t.Errorf("Subagents: got %v, want %v", actual.Subagents, expected.Subagents)
	}

	// Compare Diagnostics
	if !reflect.DeepEqual(expected.Diagnostics, actual.Diagnostics) {
		t.Errorf("Diagnostics: got %+v, want %+v", actual.Diagnostics, expected.Diagnostics)
	}
}

func floatsEqual(a, b float64) bool {
	const epsilon = 1e-6
	if a == b {
		return true
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < epsilon
}
