package handler

import (
	"github.com/peasant-labs/schema"
)

type PublishRequestFixture struct {
	Identity    IdentityFixture
	Model       ModelFixture
	Timestamp   TimestampFixture
	Source      SourceFixture
	Git         GitFixture
	Project     ProjectFixture
	Stats       StatsFixture
	Quality     *QualityFixture
	Subagents   []schema.SubagentRef
	Diagnostics DiagnosticsFixture
}

type IdentityFixture struct {
	SessionID       string
	ParentSessionID *string
	SchemaVersion   int
}

type ModelFixture struct {
	Provider       string
	Model          string
	HarnessVersion string
}

type TimestampFixture struct {
	Start    int64
	End      int64
	Ingested *int64
}

type SourceFixture struct {
	FilePath string
	Format   string
}

type GitFixture struct {
	Branch   *string
	Remote   *string
	Worktree *string
}

type ProjectFixture struct {
	Hash     string
	FilePath string
	Name     string
}

type StatsFixture struct {
	TurnCount     int
	ToolCallCount int
	SubagentCount int
	DurationMs    int64
	TokensIn      int
	TokensOut     int
}

type QualityFixture struct {
	TitleGenerated          *string
	Outcome                 *string
	FilesTouched            *int
	LinesChanged            *int
	RetryLoops              *int
	RetryTokensWasted       *int
	WithinSessionReverts    *int
	SignalDensity           *float64
	SpecQualityScore        *float64
	ExplorationRatio        *float64
	ScopeBreadth            *int
	DiscoveryTurns          *int
	M2TokenOutcomeRatio     *float64
	M3UniqueToolCount       *int
	M4ErrorRecoveryCount    *int
	M4ConsecutiveErrorMax   *int
	M5ContextUtilizationPct *float64
	M5PeakContextTokens     *int
	M5AvgMessageTokens      *int
	M6OutputSurvivalPct     *float64
	M6LinesSurvived         *int
	M6LinesTotal            *int
	M7SpecWordCount         *int
	M7SpecHasExamples       *bool
	M7SpecHasConstraints    *bool
	ComputedAt              *int64
	ComputeVersion          *int
}

type DiagnosticsFixture struct {
	Warnings []schema.DiagnosticEntry
	Partial  *bool
}

func (f PublishRequestFixture) ToSchema() schema.PublishRequest {
	req := schema.PublishRequest{
		Identity: schema.SessionIdentity{
			SessionID:     schema.SessionID(f.Identity.SessionID),
			SchemaVersion: f.Identity.SchemaVersion,
		},
		Model: schema.ModelInfo{
			Harness:        schema.Harness(f.Model.Provider),
			Model:          schema.ModelID(f.Model.Model),
			HarnessVersion: f.Model.HarnessVersion,
		},
		Timestamp: schema.TimestampInfo{
			Start: f.Timestamp.Start,
			End:   f.Timestamp.End,
		},
		Source: schema.SourceInfo{
			FilePath: f.Source.FilePath,
			Format:   schema.SourceFormat(f.Source.Format),
		},
		Git: schema.GitContext{
			Branch:   f.Git.Branch,
			Remote:   f.Git.Remote,
			Worktree: f.Git.Worktree,
		},
		Project: schema.ProjectContext{
			Hash:     schema.ProjectHash(f.Project.Hash),
			FilePath: f.Project.FilePath,
			Name:     f.Project.Name,
		},
		Stats: schema.SessionStats{
			TurnCount:     f.Stats.TurnCount,
			ToolCallCount: f.Stats.ToolCallCount,
			SubagentCount: f.Stats.SubagentCount,
			DurationMs:    f.Stats.DurationMs,
			TokensIn:      f.Stats.TokensIn,
			TokensOut:     f.Stats.TokensOut,
		},
		Subagents: f.Subagents,
		Diagnostics: schema.DiagnosticsInfo{
			Warnings: f.Diagnostics.Warnings,
			Partial:  f.Diagnostics.Partial,
		},
	}

	if f.Identity.ParentSessionID != nil {
		sid := schema.SessionID(*f.Identity.ParentSessionID)
		req.Identity.ParentSessionID = &sid
	}

	if f.Timestamp.Ingested != nil {
		req.Timestamp.Ingested = f.Timestamp.Ingested
	}

	if f.Quality != nil {
		req.Quality = f.Quality.ToSchema()
	}

	return req
}

func (f QualityFixture) ToSchema() *schema.QualityMetrics {
	q := &schema.QualityMetrics{}

	if v := f.TitleGenerated; v != nil {
		q.TitleGenerated = v
	}
	if v := f.Outcome; v != nil {
		outcome := schema.SessionOutcome(*v)
		q.Outcome = &outcome
	}
	if v := f.FilesTouched; v != nil {
		q.FilesTouched = v
	}
	if v := f.LinesChanged; v != nil {
		q.LinesChanged = v
	}
	if v := f.RetryLoops; v != nil {
		q.RetryLoops = v
	}
	if v := f.RetryTokensWasted; v != nil {
		q.RetryTokensWasted = v
	}
	if v := f.WithinSessionReverts; v != nil {
		q.WithinSessionReverts = v
	}
	if v := f.SignalDensity; v != nil {
		q.SignalDensity = v
	}
	if v := f.SpecQualityScore; v != nil {
		q.SpecQualityScore = v
	}
	if v := f.ExplorationRatio; v != nil {
		q.ExplorationRatio = v
	}
	if v := f.ScopeBreadth; v != nil {
		q.ScopeBreadth = v
	}
	if v := f.DiscoveryTurns; v != nil {
		q.DiscoveryTurns = v
	}
	if v := f.M2TokenOutcomeRatio; v != nil {
		q.M2TokenOutcomeRatio = v
	}
	if v := f.M3UniqueToolCount; v != nil {
		q.M3UniqueToolCount = v
	}
	if v := f.M4ErrorRecoveryCount; v != nil {
		q.M4ErrorRecoveryCount = v
	}
	if v := f.M4ConsecutiveErrorMax; v != nil {
		q.M4ConsecutiveErrorMax = v
	}
	if v := f.M5ContextUtilizationPct; v != nil {
		q.M5ContextUtilizationPct = v
	}
	if v := f.M5PeakContextTokens; v != nil {
		q.M5PeakContextTokens = v
	}
	if v := f.M5AvgMessageTokens; v != nil {
		q.M5AvgMessageTokens = v
	}
	if v := f.M6OutputSurvivalPct; v != nil {
		q.M6OutputSurvivalPct = v
	}
	if v := f.M6LinesSurvived; v != nil {
		q.M6LinesSurvived = v
	}
	if v := f.M6LinesTotal; v != nil {
		q.M6LinesTotal = v
	}
	if v := f.M7SpecWordCount; v != nil {
		q.M7SpecWordCount = v
	}
	if v := f.M7SpecHasExamples; v != nil {
		q.M7SpecHasExamples = v
	}
	if v := f.M7SpecHasConstraints; v != nil {
		q.M7SpecHasConstraints = v
	}
	if v := f.ComputedAt; v != nil {
		q.ComputedAt = v
	}
	if v := f.ComputeVersion; v != nil {
		q.ComputeVersion = v
	}

	return q
}

var (
	ValidMinimal = PublishRequestFixture{
		Identity: IdentityFixture{
			SessionID:     "550e8400-e29b-41d4-a716-446655440000",
			SchemaVersion: 2,
		},
		Model: ModelFixture{
			Provider: "openai",
			Model:    "gpt-4",
		},
		Timestamp: TimestampFixture{
			Start: 1700000000000,
			End:   1700000060000,
		},
		Source: SourceFixture{
			FilePath: "/path/to/transcript.jsonl",
			Format:   "jsonl",
		},
		Git: GitFixture{
			Branch: strPtr("main"),
		},
		Project: ProjectFixture{
			Hash: "abc123",
			Name: "test-project",
		},
		Stats: StatsFixture{
			TurnCount:     10,
			ToolCallCount: 25,
			DurationMs:    60000,
			TokensIn:      5000,
			TokensOut:     10000,
		},
		Diagnostics: DiagnosticsFixture{
			Warnings: []schema.DiagnosticEntry{},
		},
	}

	ValidFull = PublishRequestFixture{
		Identity: IdentityFixture{
			SessionID:       "550e8400-e29b-41d4-a716-446655440000",
			ParentSessionID: strPtr("660e8400-e29b-41d4-a716-446655440001"),
			SchemaVersion:   2,
		},
		Model: ModelFixture{
			Provider:       "openai",
			Model:          "gpt-4",
			HarnessVersion: "1.0",
		},
		Timestamp: TimestampFixture{
			Start:    1700000000000,
			End:      1700000060000,
			Ingested: int64Ptr(1700000100000),
		},
		Source: SourceFixture{
			FilePath: "/path/to/transcript.jsonl",
			Format:   "jsonl",
		},
		Git: GitFixture{
			Branch:   strPtr("main"),
			Remote:   strPtr("origin"),
			Worktree: strPtr("/path/to/worktree"),
		},
		Project: ProjectFixture{
			Hash:     "abc123def456",
			FilePath: "/path/to/project",
			Name:     "test-project",
		},
		Stats: StatsFixture{
			TurnCount:     10,
			ToolCallCount: 25,
			SubagentCount: 2,
			DurationMs:    60000,
			TokensIn:      5000,
			TokensOut:     10000,
		},
		Quality: &QualityFixture{
			TitleGenerated:      strPtr("Test Session"),
			Outcome:             strPtr("success"),
			FilesTouched:        intPtr(10),
			LinesChanged:        intPtr(100),
			SignalDensity:       float64Ptr(0.75),
			SpecQualityScore:    float64Ptr(0.9),
			M2TokenOutcomeRatio: float64Ptr(0.85),
			M3UniqueToolCount:   intPtr(15),
		},
		Subagents: []schema.SubagentRef{
			{SessionID: "ses_001", ParentUUID: "550e8400-e29b-41d4-a716-446655440000"},
		},
		Diagnostics: DiagnosticsFixture{
			Partial: boolPtr(true),
		},
	}

	ZeroValues = PublishRequestFixture{
		Identity: IdentityFixture{
			SessionID:     "00000000-0000-0000-0000-000000000000",
			SchemaVersion: 0,
		},
		Model: ModelFixture{
			Provider: "",
			Model:    "",
		},
		Timestamp: TimestampFixture{
			Start: 0,
			End:   0,
		},
		Source: SourceFixture{
			FilePath: "",
			Format:   "",
		},
		Git:         GitFixture{},
		Project:     ProjectFixture{Hash: "", Name: ""},
		Stats:       StatsFixture{},
		Diagnostics: DiagnosticsFixture{},
	}

	NegativeTimes = PublishRequestFixture{
		Identity: IdentityFixture{
			SessionID:     "550e8400-e29b-41d4-a716-446655440000",
			SchemaVersion: 2,
		},
		Model: ModelFixture{
			Provider: "openai",
			Model:    "gpt-4",
		},
		Timestamp: TimestampFixture{
			Start:    -1000,
			End:      -500,
			Ingested: int64Ptr(-100),
		},
		Source: SourceFixture{
			FilePath: "/path/to/transcript.jsonl",
			Format:   "jsonl",
		},
		Stats: StatsFixture{
			TurnCount: 10,
		},
		Diagnostics: DiagnosticsFixture{},
	}
)

func strPtr(s string) *string       { return &s }
func intPtr(i int) *int             { return &i }
func int64Ptr(i int64) *int64       { return &i }
func float64Ptr(f float64) *float64 { return &f }
func boolPtr(b bool) *bool          { return &b }
