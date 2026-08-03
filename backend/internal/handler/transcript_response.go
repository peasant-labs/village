package handler

import (
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// transcriptResponse is the single public projection of a transcript row.
// Storage locations and source-machine paths remain internal database state.
type transcriptResponse struct {
	ID                      pgtype.UUID        `json:"id"`
	OwnerID                 pgtype.UUID        `json:"owner_id"`
	LocalID                 string             `json:"local_id"`
	Title                   pgtype.Text        `json:"title"`
	Description             pgtype.Text        `json:"description"`
	Visibility              string             `json:"visibility"`
	ModelProvider           string             `json:"model_provider"`
	ModelName               pgtype.Text        `json:"model_name"`
	HarnessVersion          pgtype.Text        `json:"harness_version"`
	SessionStart            pgtype.Timestamptz `json:"session_start"`
	SessionEnd              pgtype.Timestamptz `json:"session_end"`
	TurnCount               pgtype.Int4        `json:"turn_count"`
	TokenCount              pgtype.Int4        `json:"token_count"`
	BlobSizeBytes           pgtype.Int8        `json:"blob_size_bytes"`
	SchemaVersion           string             `json:"schema_version"`
	PublishedAt             pgtype.Timestamptz `json:"published_at"`
	UpdatedAt               pgtype.Timestamptz `json:"updated_at"`
	ParentSessionID         pgtype.Text        `json:"parent_session_id"`
	IngestedAt              pgtype.Timestamptz `json:"ingested_at"`
	SourceFormat            pgtype.Text        `json:"source_format"`
	GitBranch               pgtype.Text        `json:"git_branch"`
	GitRemote               pgtype.Text        `json:"git_remote"`
	ProjectHash             pgtype.Text        `json:"project_hash"`
	ProjectName             pgtype.Text        `json:"project_name"`
	ToolCallCount           pgtype.Int4        `json:"tool_call_count"`
	SubagentCount           pgtype.Int4        `json:"subagent_count"`
	DurationMs              pgtype.Int8        `json:"duration_ms"`
	Subagents               []byte             `json:"subagents"`
	DiagnosticsWarnings     []byte             `json:"diagnostics_warnings"`
	DiagnosticsPartial      pgtype.Bool        `json:"diagnostics_partial"`
	TokensIn                pgtype.Int8        `json:"tokens_in"`
	TokensOut               pgtype.Int8        `json:"tokens_out"`
	TitleGenerated          pgtype.Text        `json:"title_generated"`
	Outcome                 pgtype.Text        `json:"outcome"`
	FilesTouched            pgtype.Int4        `json:"files_touched"`
	LinesChanged            pgtype.Int4        `json:"lines_changed"`
	RetryLoops              pgtype.Int4        `json:"retry_loops"`
	RetryTokensWasted       pgtype.Int4        `json:"retry_tokens_wasted"`
	WithinSessionReverts    pgtype.Int4        `json:"within_session_reverts"`
	SignalDensity           pgtype.Float4      `json:"signal_density"`
	SpecQualityScore        pgtype.Float4      `json:"spec_quality_score"`
	ExplorationRatio        pgtype.Float4      `json:"exploration_ratio"`
	ScopeBreadth            pgtype.Int4        `json:"scope_breadth"`
	DiscoveryTurns          pgtype.Int4        `json:"discovery_turns"`
	M2TokenOutcomeRatio     pgtype.Float4      `json:"m2_token_outcome_ratio"`
	M3UniqueToolCount       pgtype.Int4        `json:"m3_unique_tool_count"`
	M4ErrorRecoveryCount    pgtype.Int4        `json:"m4_error_recovery_count"`
	M4ConsecutiveErrorMax   pgtype.Int4        `json:"m4_consecutive_error_max"`
	M5ContextUtilizationPct pgtype.Float4      `json:"m5_context_utilization_pct"`
	M5PeakContextTokens     pgtype.Int4        `json:"m5_peak_context_tokens"`
	M5AvgMessageTokens      pgtype.Int4        `json:"m5_avg_message_tokens"`
	M6OutputSurvivalPct     pgtype.Float4      `json:"m6_output_survival_pct"`
	M6LinesSurvived         pgtype.Int4        `json:"m6_lines_survived"`
	M6LinesTotal            pgtype.Int4        `json:"m6_lines_total"`
	M7SpecWordCount         pgtype.Int4        `json:"m7_spec_word_count"`
	M7SpecHasExamples       pgtype.Bool        `json:"m7_spec_has_examples"`
	M7SpecHasConstraints    pgtype.Bool        `json:"m7_spec_has_constraints"`
	ComputedAt              pgtype.Timestamptz `json:"computed_at"`
	ComputeVersion          pgtype.Int4        `json:"compute_version"`
	ContentHash             pgtype.Text        `json:"content_hash"`
	LicenseID               pgtype.Text        `json:"license_id"`
}

func toTranscriptResponse(r sqlc.Transcript) transcriptResponse {
	return transcriptResponse{
		ID: r.ID, OwnerID: r.OwnerID, LocalID: r.LocalID, Title: r.Title, Description: r.Description, Visibility: r.Visibility, ModelProvider: r.ModelProvider, ModelName: r.ModelName, HarnessVersion: r.HarnessVersion,
		SessionStart: r.SessionStart, SessionEnd: r.SessionEnd, TurnCount: r.TurnCount, TokenCount: r.TokenCount, BlobSizeBytes: r.BlobSizeBytes, SchemaVersion: r.SchemaVersion, PublishedAt: r.PublishedAt, UpdatedAt: r.UpdatedAt,
		ParentSessionID: r.ParentSessionID, IngestedAt: r.IngestedAt, SourceFormat: r.SourceFormat, GitBranch: r.GitBranch, GitRemote: r.GitRemote, ProjectHash: r.ProjectHash, ProjectName: r.ProjectName,
		ToolCallCount: r.ToolCallCount, SubagentCount: r.SubagentCount, DurationMs: r.DurationMs, Subagents: r.Subagents, DiagnosticsWarnings: r.DiagnosticsWarnings, DiagnosticsPartial: r.DiagnosticsPartial,
		TokensIn: r.TokensIn, TokensOut: r.TokensOut, TitleGenerated: r.TitleGenerated, Outcome: r.Outcome, FilesTouched: r.FilesTouched, LinesChanged: r.LinesChanged, RetryLoops: r.RetryLoops, RetryTokensWasted: r.RetryTokensWasted,
		WithinSessionReverts: r.WithinSessionReverts, SignalDensity: r.SignalDensity, SpecQualityScore: r.SpecQualityScore, ExplorationRatio: r.ExplorationRatio, ScopeBreadth: r.ScopeBreadth, DiscoveryTurns: r.DiscoveryTurns,
		M2TokenOutcomeRatio: r.M2TokenOutcomeRatio, M3UniqueToolCount: r.M3UniqueToolCount, M4ErrorRecoveryCount: r.M4ErrorRecoveryCount, M4ConsecutiveErrorMax: r.M4ConsecutiveErrorMax,
		M5ContextUtilizationPct: r.M5ContextUtilizationPct, M5PeakContextTokens: r.M5PeakContextTokens, M5AvgMessageTokens: r.M5AvgMessageTokens, M6OutputSurvivalPct: r.M6OutputSurvivalPct,
		M6LinesSurvived: r.M6LinesSurvived, M6LinesTotal: r.M6LinesTotal, M7SpecWordCount: r.M7SpecWordCount, M7SpecHasExamples: r.M7SpecHasExamples, M7SpecHasConstraints: r.M7SpecHasConstraints,
		ComputedAt: r.ComputedAt, ComputeVersion: r.ComputeVersion, ContentHash: r.ContentHash, LicenseID: r.LicenseID,
	}
}

// These narrow composition points keep each mounted envelope wired to the one
// field-policy mapper without creating route-specific projections.
func publishTranscriptResponse(row sqlc.Transcript) transcriptResponse {
	return toTranscriptResponse(row)
}

func detailTranscriptResponse(row sqlc.Transcript) transcriptResponse {
	return toTranscriptResponse(row)
}

func updateTranscriptResponse(row sqlc.Transcript) transcriptResponse {
	return toTranscriptResponse(row)
}

func listTranscriptResponse(row sqlc.Transcript) transcriptResponse {
	return toTranscriptResponse(row)
}

type groupTranscriptResponse struct {
	transcriptResponse
	OwnerUsername       pgtype.Text `json:"owner_username"`
	OwnerAvatarURL      pgtype.Text `json:"owner_avatar_url"`
	OwnerIsDiscoverable bool        `json:"owner_is_discoverable"`
}

func groupTranscriptFromRow(row sqlc.ListGroupTranscriptsRow) groupTranscriptResponse {
	dbRow := sqlc.Transcript{
		ID: row.ID, OwnerID: row.OwnerID, LocalID: row.LocalID, Title: row.Title, Description: row.Description, Visibility: row.Visibility,
		ModelProvider: row.ModelProvider, ModelName: row.ModelName, HarnessVersion: row.HarnessVersion, SessionStart: row.SessionStart, SessionEnd: row.SessionEnd,
		TurnCount: row.TurnCount, TokenCount: row.TokenCount, BlobKey: row.BlobKey, BlobSizeBytes: row.BlobSizeBytes, SchemaVersion: row.SchemaVersion,
		PublishedAt: row.PublishedAt, UpdatedAt: row.UpdatedAt, ParentSessionID: row.ParentSessionID, IngestedAt: row.IngestedAt,
		SourceFilePath: row.SourceFilePath, SourceFormat: row.SourceFormat, GitBranch: row.GitBranch, GitRemote: row.GitRemote, GitWorktree: row.GitWorktree,
		ProjectHash: row.ProjectHash, ProjectPath: row.ProjectPath, ProjectName: row.ProjectName, ToolCallCount: row.ToolCallCount, SubagentCount: row.SubagentCount,
		DurationMs: row.DurationMs, Subagents: row.Subagents, DiagnosticsWarnings: row.DiagnosticsWarnings, DiagnosticsPartial: row.DiagnosticsPartial,
		TokensIn: row.TokensIn, TokensOut: row.TokensOut, TitleGenerated: row.TitleGenerated, Outcome: row.Outcome, FilesTouched: row.FilesTouched,
		LinesChanged: row.LinesChanged, RetryLoops: row.RetryLoops, RetryTokensWasted: row.RetryTokensWasted, WithinSessionReverts: row.WithinSessionReverts,
		SignalDensity: row.SignalDensity, SpecQualityScore: row.SpecQualityScore, ExplorationRatio: row.ExplorationRatio, ScopeBreadth: row.ScopeBreadth,
		DiscoveryTurns: row.DiscoveryTurns, M2TokenOutcomeRatio: row.M2TokenOutcomeRatio, M3UniqueToolCount: row.M3UniqueToolCount,
		M4ErrorRecoveryCount: row.M4ErrorRecoveryCount, M4ConsecutiveErrorMax: row.M4ConsecutiveErrorMax, M5ContextUtilizationPct: row.M5ContextUtilizationPct,
		M5PeakContextTokens: row.M5PeakContextTokens, M5AvgMessageTokens: row.M5AvgMessageTokens, M6OutputSurvivalPct: row.M6OutputSurvivalPct,
		M6LinesSurvived: row.M6LinesSurvived, M6LinesTotal: row.M6LinesTotal, M7SpecWordCount: row.M7SpecWordCount,
		M7SpecHasExamples: row.M7SpecHasExamples, M7SpecHasConstraints: row.M7SpecHasConstraints, ComputedAt: row.ComputedAt,
		ComputeVersion: row.ComputeVersion, ContentHash: row.ContentHash, LicenseID: row.LicenseID,
		WrappedDataKey: row.WrappedDataKey, EncryptionAlgorithm: row.EncryptionAlgorithm, KeyVersion: row.KeyVersion,
	}
	return groupTranscriptResponse{
		transcriptResponse:  toTranscriptResponse(dbRow),
		OwnerUsername:       pgtype.Text{String: row.OwnerUsername, Valid: row.OwnerUsername != ""},
		OwnerAvatarURL:      row.OwnerAvatarUrl,
		OwnerIsDiscoverable: row.OwnerIsDiscoverable,
	}
}
