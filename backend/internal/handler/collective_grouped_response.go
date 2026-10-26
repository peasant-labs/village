package handler

import (
	"time"

	"github.com/peasant-labs/schema"
)

// collectiveSessionRow derives the collective arm from the same authorized
// session as the common row. No second metadata read can mix two revisions.
// Its pull request summary starts empty and is filled for the rows of the page
// being served, with one query for the whole page
// (fillCollectivePullRequestSummaries).
//
// owner is the transcript's owner as the viewer may see them
// (resolvePublicOwners); the row and its arm carry the same value.
func collectiveSessionRow(s schema.VillageTranscript, owner schema.VillagePublicOwner) schema.VillageSessionRow {
	return schema.VillageSessionRow{Session: s, Owner: owner, Collective: &schema.VillageCollectiveTranscript{
		ID: s.ID, LocalID: s.LocalID,
		Title: s.Title, Description: s.Description, Visibility: s.Visibility,
		ModelProvider: s.ModelProvider, ModelName: s.ModelName, HarnessVersion: s.HarnessVersion,
		SessionStart: s.SessionStart, SessionEnd: s.SessionEnd, TurnCount: s.TurnCount,
		TokenCount: s.TokenCount, BlobSizeBytes: s.BlobSizeBytes, SchemaVersion: s.SchemaVersion,
		PublishedAt: s.PublishedAt, UpdatedAt: s.UpdatedAt, ParentSessionID: s.ParentSessionID,
		ParentTranscriptID: s.ParentTranscriptID, IngestedAt: s.IngestedAt, SourceFormat: s.SourceFormat, GitBranch: s.GitBranch,
		GitRemote: s.GitRemote, ProjectHash: s.ProjectHash, ProjectName: s.ProjectName,
		ProjectDisplayName: s.ProjectDisplayName, ProjectNameSource: s.ProjectNameSource,
		ProjectRemoteLabel: s.ProjectRemoteLabel, ToolCallCount: s.ToolCallCount,
		SubagentCount: s.SubagentCount, DurationMs: s.DurationMs, Subagents: s.Subagents,
		DiagnosticsWarnings: s.DiagnosticsWarnings, DiagnosticsPartial: s.DiagnosticsPartial,
		TokensIn: s.TokensIn, TokensOut: s.TokensOut, TitleGenerated: s.TitleGenerated,
		Outcome: s.Outcome, FilesTouched: s.FilesTouched, LinesChanged: s.LinesChanged,
		RetryLoops: s.RetryLoops, RetryTokensWasted: s.RetryTokensWasted,
		WithinSessionReverts: s.WithinSessionReverts, SignalDensity: s.SignalDensity,
		SpecQualityScore: s.SpecQualityScore, ExplorationRatio: s.ExplorationRatio,
		ScopeBreadth: s.ScopeBreadth, DiscoveryTurns: s.DiscoveryTurns,
		M2TokenOutcomeRatio: s.M2TokenOutcomeRatio, M3UniqueToolCount: s.M3UniqueToolCount,
		M4ErrorRecoveryCount: s.M4ErrorRecoveryCount, M4ConsecutiveErrorMax: s.M4ConsecutiveErrorMax,
		M5ContextUtilizationPct: s.M5ContextUtilizationPct, M5PeakContextTokens: s.M5PeakContextTokens,
		M5AvgMessageTokens: s.M5AvgMessageTokens, M6OutputSurvivalPct: s.M6OutputSurvivalPct,
		M6LinesSurvived: s.M6LinesSurvived, M6LinesTotal: s.M6LinesTotal,
		M7SpecWordCount: s.M7SpecWordCount, M7SpecHasExamples: s.M7SpecHasExamples,
		M7SpecHasConstraints: s.M7SpecHasConstraints, ComputedAt: s.ComputedAt,
		ComputeVersion: s.ComputeVersion, ContentHash: s.ContentHash, LicenseID: s.LicenseID,
		SessionOrigin: s.SessionOrigin, InputSubmissionCount: s.InputSubmissionCount,
		RootSessionID: s.RootSessionID, Purpose: s.Purpose, Relationships: s.Relationships,
		Owner:        owner,
		PullRequests: emptyPullRequestsSummary(),
	}}
}

func pendingSessionRow(s schema.VillageTranscript, owner schema.VillagePublicOwner, sharedAt time.Time) schema.VillageSessionRow {
	return schema.VillageSessionRow{Session: s, Owner: owner, Pending: &schema.VillagePendingShare{
		TranscriptID: s.ID, Title: s.Title, ModelProvider: s.ModelProvider,
		Owner: owner, LocalID: s.LocalID, ParentSessionID: s.ParentSessionID, ParentTranscriptID: s.ParentTranscriptID,
		ProjectHash: s.ProjectHash, ProjectName: s.ProjectName, Branch: s.GitBranch,
		SharedAt:             sharedAt,
		InputSubmissionCount: s.InputSubmissionCount, RootSessionID: s.RootSessionID,
		Purpose: s.Purpose, Relationships: s.Relationships,
	}}
}

func myShareSessionRow(s schema.VillageTranscript, owner schema.VillagePublicOwner, status schema.VillageShareStatus, sharedAt time.Time) schema.VillageSessionRow {
	return schema.VillageSessionRow{Session: s, Owner: owner, MyShare: &schema.VillageUserCollectiveShare{
		ID: s.ID, Title: s.Title, ModelProvider: s.ModelProvider, ModelName: s.ModelName,
		Visibility: s.Visibility, PublishedAt: s.PublishedAt, TurnCount: s.TurnCount,
		TokensIn: s.TokensIn, TokensOut: s.TokensOut, Owner: owner, LocalID: s.LocalID,
		ParentSessionID: s.ParentSessionID, ParentTranscriptID: s.ParentTranscriptID, Status: status, SharedAt: sharedAt,
		InputSubmissionCount: s.InputSubmissionCount, RootSessionID: s.RootSessionID,
		Purpose: s.Purpose, Relationships: s.Relationships,
	}}
}

// derefOr reads a repository field of the caller's own contributable row. The
// caller always sees their own profile, so these are never withheld there;
// the contract's row validation refuses a mismatch should one ever be null.
func derefOr[T any](value *T) T {
	var zero T
	if value == nil {
		return zero
	}
	return *value
}

func contributableSessionRow(s schema.VillageTranscript, owner schema.VillagePublicOwner, alreadyShared bool) schema.VillageSessionRow {
	return schema.VillageSessionRow{Session: s, Owner: owner, Contributable: &schema.VillageContributableTranscript{
		ID: s.ID, LocalID: s.LocalID, Title: s.Title, Visibility: s.Visibility,
		ProjectHash: derefOr(s.ProjectHash), ProjectDisplayName: derefOr(s.ProjectDisplayName),
		ProjectNameSource: derefOr(s.ProjectNameSource), GitBranch: s.GitBranch,
		ParentSessionID: s.ParentSessionID, SessionOrigin: s.SessionOrigin,
		ModelProvider: s.ModelProvider, PublishedAt: s.PublishedAt, AlreadyShared: alreadyShared,
		InputSubmissionCount: s.InputSubmissionCount, RootSessionID: s.RootSessionID,
		Purpose: s.Purpose, Relationships: s.Relationships,
	}}
}
