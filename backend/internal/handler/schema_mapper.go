package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
)

const DefaultVisibility = "private"

// ErrPublishProjectHashMissing marks a publish refused for carrying no project
// identity. It is a distinct sentinel so the publish handler can answer 422 for it
// without string-matching the message.
var ErrPublishProjectHashMissing = errors.New("publish metadata carries no project hash")

// validatePublishProjectIdentity is the publish trust boundary's guard on project
// identity: a transcript may not be stored unless it says which project it belongs
// to.
//
// The hash is the identity. A name can change, and a privacy-conscious client may
// withhold it entirely, so a name cannot say whether two transcripts came from the
// same project; the hash can, and every grouping and routing surface keys on it.
// A row with no hash would belong to no project and appear in no project's history.
//
// The published contract already rejects a project object that is PRESENT but
// hash-less. The hole this closes is the payload that omits the project object
// altogether, which the contract's top level still permits.
func validatePublishProjectIdentity(req schema.PublishRequest) error {
	if strings.TrimSpace(string(req.Project.Hash)) != "" {
		return nil
	}
	return fmt.Errorf(
		"%w: the metadata contains no project.hash. "+
			"A project's identity, not its name, is what groups a publisher's transcripts, and a name may be renamed or "+
			"withheld for privacy, so every published transcript must carry the hash. "+
			"The request was refused at Village's publish trust boundary, "+
			"before any transcript row, blob, or share was written, "+
			"so nothing was stored and no transcript you published earlier was changed. "+
			"Upgrade the peasant CLI to a version that sends the project object, then push this session again",
		ErrPublishProjectHashMissing)
}

// schemaToTranscriptParams converts one publish request into database params.
// origin is the already-resolved session origin: metadata alone cannot say who
// drove a session, so that answer is decided on the publish path -- from the
// producer's declaration when it made one, from this server's classifier over
// the accepted bytes when it did not -- and handed in. The mapper neither
// decides it nor seeds a placeholder for a later overwrite, so the value it
// returns is the value stored.
func schemaToTranscriptParams(req schema.PublishRequest, blobKey string, blobSize int64, schemaVersion string, origin sessionorigin.Origin) sqlc.CreateTranscriptParams {
	q := qualityToParams(req.Quality)

	return sqlc.CreateTranscriptParams{
		ID:            toPgUUID(uuid.New()),
		Title:         deriveTitle(req),
		Description:   pgtype.Text{Valid: false},
		Visibility:    DefaultVisibility,
		SessionOrigin: origin.String(),
		// License is captured at publish (create) from the producer-stamped
		// req.License. The schema module's embedded SchemaLicense enum already
		// rejected any out-of-menu value at the 422 gate, so this is either empty
		// (→ NULL, legacy/unset) or a valid seeded licenses.id (FK-safe). Changing
		// it after publish is governed separately and writes an audit event.
		LicenseID:               pgtype.Text{String: string(req.License), Valid: req.License != ""},
		ModelProvider:           string(req.Model.Harness),
		ModelName:               pgtype.Text{String: string(req.Model.Model), Valid: req.Model.Model != ""},
		HarnessVersion:          pgtype.Text{String: req.Model.HarnessVersion, Valid: req.Model.HarnessVersion != ""},
		SessionStart:            int64ToTimestamptz(req.Timestamp.Start),
		SessionEnd:              int64ToTimestamptz(req.Timestamp.End),
		TurnCount:               intToPgInt4(req.Stats.TurnCount),
		TokenCount:              pgtype.Int4{Valid: false},
		BlobKey:                 blobKey,
		BlobSizeBytes:           int64ToPgInt8(blobSize),
		SchemaVersion:           schemaVersion,
		ParentSessionID:         sessionIDPtrToPgText(req.Identity.ParentSessionID),
		IngestedAt:              int64PtrToTimestamptz(req.Timestamp.Ingested),
		SourceFilePath:          requiredStringToPgText(req.Source.FilePath),
		SourceFormat:            pgtype.Text{String: string(req.Source.Format), Valid: req.Source.Format != ""},
		GitBranch:               optionalStringToPgText(req.Git.Branch),
		GitRemote:               optionalStringToPgText(req.Git.Remote),
		GitWorktree:             optionalStringToPgText(req.Git.Worktree),
		ProjectHash:             string(req.Project.Hash),
		ProjectPath:             pgtype.Text{String: req.Project.FilePath, Valid: req.Project.FilePath != ""},
		ProjectName:             pgtype.Text{String: req.Project.Name, Valid: req.Project.Name != ""},
		ToolCallCount:           intToPgInt4(req.Stats.ToolCallCount),
		SubagentCount:           intToPgInt4(req.Stats.SubagentCount),
		DurationMs:              int64ToPgInt8(req.Stats.DurationMs),
		Subagents:               marshalSubagents(req.Subagents),
		DiagnosticsWarnings:     marshalDiagnosticsWarnings(req.Diagnostics.Warnings),
		DiagnosticsPartial:      toPgBool(req.Diagnostics.Partial),
		TokensIn:                intToPgInt8(req.Stats.TokensIn),
		TokensOut:               intToPgInt8(req.Stats.TokensOut),
		TitleGenerated:          q.TitleGenerated,
		Outcome:                 q.Outcome,
		FilesTouched:            q.FilesTouched,
		LinesChanged:            q.LinesChanged,
		RetryLoops:              q.RetryLoops,
		RetryTokensWasted:       q.RetryTokensWasted,
		WithinSessionReverts:    q.WithinSessionReverts,
		SignalDensity:           q.SignalDensity,
		SpecQualityScore:        q.SpecQualityScore,
		ExplorationRatio:        q.ExplorationRatio,
		ScopeBreadth:            q.ScopeBreadth,
		DiscoveryTurns:          q.DiscoveryTurns,
		M2TokenOutcomeRatio:     q.M2TokenOutcomeRatio,
		M3UniqueToolCount:       q.M3UniqueToolCount,
		M4ErrorRecoveryCount:    q.M4ErrorRecoveryCount,
		M4ConsecutiveErrorMax:   q.M4ConsecutiveErrorMax,
		M5ContextUtilizationPct: q.M5ContextUtilizationPct,
		M5PeakContextTokens:     q.M5PeakContextTokens,
		M5AvgMessageTokens:      q.M5AvgMessageTokens,
		M6OutputSurvivalPct:     q.M6OutputSurvivalPct,
		M6LinesSurvived:         q.M6LinesSurvived,
		M6LinesTotal:            q.M6LinesTotal,
		M7SpecWordCount:         q.M7SpecWordCount,
		M7SpecHasExamples:       q.M7SpecHasExamples,
		M7SpecHasConstraints:    q.M7SpecHasConstraints,
		ComputedAt:              q.ComputedAt,
		ComputeVersion:          q.ComputeVersion,
	}
}

// schemaToCommitRecords converts the publish payload's gitContext.commits[] into
// the per-commit records consumed by the batched InsertTranscriptCommits upsert
// (jsonb_to_recordset). The payload (schema.CommitInfo) carries a SHA, message,
// author name/email, and author/commit timestamps — but no additions/deletions
// counts, so those stay null. Empty strings and unset (negative) times map to
// JSON null (a SQL NULL column). Commits with an empty SHA are skipped (SHA is
// the dedup key); commit_order is the commit's index in the original payload.
func schemaToCommitRecords(commits []schema.CommitInfo) []commitJSONRecord {
	if len(commits) == 0 {
		return nil
	}
	records := make([]commitJSONRecord, 0, len(commits))
	for i, c := range commits {
		if c.Hash == "" {
			continue
		}
		records = append(records, commitJSONRecord{
			CommitOrder: int32(i),
			Sha:         c.Hash,
			Message:     optionalString(c.Message),
			AuthorName:  optionalString(c.AuthorName),
			AuthorEmail: optionalString(c.AuthorEmail),
			Additions:   nil, // payload carries no line counts
			Deletions:   nil,
			AuthoredAt:  optionalMillis(c.AuthorTime),
			CommittedAt: optionalMillis(c.CommitTime),
		})
	}
	return records
}

// extractRepoName derives a clean project display name.
// It prefers parsing the git remote URL (e.g. "github.com/example-org/sample-app.git" → "sample-app"),
// falling back to stripping known directory prefixes from the dash-delimited project name key
// (e.g. "-Users-developer-Documents-GitHub-sample-app" → "sample-app").
func extractRepoName(projectName string, gitRemote string) string {
	// Try git remote first
	if gitRemote != "" {
		remote := strings.TrimSuffix(gitRemote, ".git")
		if idx := strings.LastIndex(remote, "/"); idx >= 0 {
			name := remote[idx+1:]
			if name != "" {
				return name
			}
		}
	}

	if projectName == "" {
		return ""
	}

	// Slash-separated path: take last segment
	if strings.Contains(projectName, "/") {
		parts := strings.Split(projectName, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] != "" {
				return parts[i]
			}
		}
	}

	// Dash-delimited path key (e.g. "-Users-developer-Documents-GitHub-project-name")
	// Strip leading dash, split into segments, find the last known directory
	// marker and take everything after it as the project name.
	knownDirs := []string{"github", "documents", "projects", "repos", "src", "code", "dev", "home"}
	stripped := strings.TrimPrefix(projectName, "-")
	segments := strings.Split(stripped, "-")

	lastKnownIdx := -1
	for i, seg := range segments {
		lower := strings.ToLower(seg)
		for _, d := range knownDirs {
			if lower == d {
				lastKnownIdx = i
				break
			}
		}
	}

	if lastKnownIdx >= 0 && lastKnownIdx < len(segments)-1 {
		return strings.Join(segments[lastKnownIdx+1:], "-")
	}

	return projectName
}

// formatModelShort strips date suffixes and title-cases model identifiers.
// e.g. "claude-opus-4-5-20251101" → "Claude Opus 4.5"
// e.g. "claude-sonnet-4-6" → "Claude Sonnet 4.6"
func formatModelShort(provider, model string) string {
	if model == "" {
		if provider != "" {
			return titleCase(strings.ToLower(provider))
		}
		return ""
	}

	s := model
	// Strip date suffix (8+ digits at end after a dash)
	if idx := strings.LastIndex(s, "-"); idx > 0 {
		suffix := s[idx+1:]
		if len(suffix) >= 8 && isAllDigits(suffix) {
			s = s[:idx]
		}
	}

	// Replace dashes with spaces
	parts := strings.Split(s, "-")

	// Try to combine version number parts (e.g. ["4", "5"] → "4.5")
	var result []string
	i := 0
	for i < len(parts) {
		if i+1 < len(parts) && isAllDigits(parts[i]) && isAllDigits(parts[i+1]) {
			result = append(result, parts[i]+"."+parts[i+1])
			i += 2
		} else {
			result = append(result, parts[i])
			i++
		}
	}

	// Title-case each word
	for j, w := range result {
		if w == "" {
			continue
		}
		runes := []rune(w)
		runes[0] = unicode.ToUpper(runes[0])
		result[j] = string(runes)
	}

	return strings.Join(result, " ")
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// deriveTitle generates a title from publish request metadata.
// Priority: 1) quality.title_generated, 2) project+model combo, 3) NULL.
func deriveTitle(req schema.PublishRequest) pgtype.Text {
	// 1. Use generated title if available
	if req.Quality != nil && req.Quality.TitleGenerated != nil && *req.Quality.TitleGenerated != "" {
		return pgtype.Text{String: *req.Quality.TitleGenerated, Valid: true}
	}

	// 2. Derive from project + model
	gitRemote := ""
	if req.Git.Remote != nil {
		gitRemote = *req.Git.Remote
	}
	repoName := extractRepoName(req.Project.Name, gitRemote)
	modelShort := formatModelShort(string(req.Model.Harness), string(req.Model.Model))

	if repoName != "" && modelShort != "" {
		return pgtype.Text{String: repoName + " - " + modelShort + " session", Valid: true}
	}
	if repoName != "" {
		return pgtype.Text{String: repoName + " session", Valid: true}
	}
	if modelShort != "" {
		return pgtype.Text{String: modelShort + " session", Valid: true}
	}

	// 3. Fall back to NULL
	return pgtype.Text{Valid: false}
}

func optionalStringToPgText(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *v, Valid: true}
}

func sessionIDPtrToPgText(v *schema.SessionID) pgtype.Text {
	if v == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: string(*v), Valid: true}
}

func requiredStringToPgText(v string) pgtype.Text {
	if v == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: v, Valid: true}
}

func qualityToParams(q *schema.QualityMetrics) qualityParams {
	if q == nil {
		return qualityParams{}
	}
	return qualityParams{
		TitleGenerated:          stringPtrToPgText(q.TitleGenerated),
		Outcome:                 outcomeToPgTextPtr(q.Outcome),
		FilesTouched:            intPtrToPgInt4(q.FilesTouched),
		LinesChanged:            intPtrToPgInt4(q.LinesChanged),
		RetryLoops:              intPtrToPgInt4(q.RetryLoops),
		RetryTokensWasted:       intPtrToPgInt4(q.RetryTokensWasted),
		WithinSessionReverts:    intPtrToPgInt4(q.WithinSessionReverts),
		SignalDensity:           float64PtrToPgFloat4(q.SignalDensity),
		SpecQualityScore:        float64PtrToPgFloat4(q.SpecQualityScore),
		ExplorationRatio:        float64PtrToPgFloat4(q.ExplorationRatio),
		ScopeBreadth:            intPtrToPgInt4(q.ScopeBreadth),
		DiscoveryTurns:          intPtrToPgInt4(q.DiscoveryTurns),
		M2TokenOutcomeRatio:     float64PtrToPgFloat4(q.M2TokenOutcomeRatio),
		M3UniqueToolCount:       intPtrToPgInt4(q.M3UniqueToolCount),
		M4ErrorRecoveryCount:    intPtrToPgInt4(q.M4ErrorRecoveryCount),
		M4ConsecutiveErrorMax:   intPtrToPgInt4(q.M4ConsecutiveErrorMax),
		M5ContextUtilizationPct: float64PtrToPgFloat4(q.M5ContextUtilizationPct),
		M5PeakContextTokens:     intPtrToPgInt4(q.M5PeakContextTokens),
		M5AvgMessageTokens:      intPtrToPgInt4(q.M5AvgMessageTokens),
		M6OutputSurvivalPct:     float64PtrToPgFloat4(q.M6OutputSurvivalPct),
		M6LinesSurvived:         intPtrToPgInt4(q.M6LinesSurvived),
		M6LinesTotal:            intPtrToPgInt4(q.M6LinesTotal),
		M7SpecWordCount:         intPtrToPgInt4(q.M7SpecWordCount),
		M7SpecHasExamples:       boolPtrToPgBool(q.M7SpecHasExamples),
		M7SpecHasConstraints:    boolPtrToPgBool(q.M7SpecHasConstraints),
		ComputedAt:              int64PtrToTimestamptz(q.ComputedAt),
		ComputeVersion:          intPtrToPgInt4(q.ComputeVersion),
	}
}

type qualityParams struct {
	TitleGenerated          pgtype.Text
	Outcome                 pgtype.Text
	FilesTouched            pgtype.Int4
	LinesChanged            pgtype.Int4
	RetryLoops              pgtype.Int4
	RetryTokensWasted       pgtype.Int4
	WithinSessionReverts    pgtype.Int4
	SignalDensity           pgtype.Float4
	SpecQualityScore        pgtype.Float4
	ExplorationRatio        pgtype.Float4
	ScopeBreadth            pgtype.Int4
	DiscoveryTurns          pgtype.Int4
	M2TokenOutcomeRatio     pgtype.Float4
	M3UniqueToolCount       pgtype.Int4
	M4ErrorRecoveryCount    pgtype.Int4
	M4ConsecutiveErrorMax   pgtype.Int4
	M5ContextUtilizationPct pgtype.Float4
	M5PeakContextTokens     pgtype.Int4
	M5AvgMessageTokens      pgtype.Int4
	M6OutputSurvivalPct     pgtype.Float4
	M6LinesSurvived         pgtype.Int4
	M6LinesTotal            pgtype.Int4
	M7SpecWordCount         pgtype.Int4
	M7SpecHasExamples       pgtype.Bool
	M7SpecHasConstraints    pgtype.Bool
	ComputedAt              pgtype.Timestamptz
	ComputeVersion          pgtype.Int4
}

func outcomeToPgTextPtr(o *schema.SessionOutcome) pgtype.Text {
	if o == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: string(*o), Valid: true}
}

func marshalSubagents(s []schema.SubagentRef) []byte {
	if len(s) == 0 {
		return nil
	}
	b, _ := json.Marshal(s)
	return b
}

func marshalDiagnosticsWarnings(w []schema.DiagnosticEntry) []byte {
	if len(w) == 0 {
		return nil
	}
	b, _ := json.Marshal(w)
	return b
}

func int64ToTimestamptz(ms int64) pgtype.Timestamptz {
	// 0 Unix ms is valid (Jan 1, 1970), but treat as "not provided" only if negative
	if ms < 0 {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: time.UnixMilli(ms), Valid: true}
}

func int64PtrToTimestamptz(ms *int64) pgtype.Timestamptz {
	if ms == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	// 0 Unix ms is valid (Jan 1, 1970), but treat as "not provided" only if negative
	if *ms < 0 {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: time.UnixMilli(*ms), Valid: true}
}

func intToPgInt4(v int) pgtype.Int4 {
	// 0 is a valid value - we can't distinguish "not provided" from 0 with primitives
	// The caller should use pointers (intPtrToPgInt4) for optional fields
	return pgtype.Int4{Int32: int32(v), Valid: true}
}

func intPtrToPgInt4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}

func intToPgInt8(v int) pgtype.Int8 {
	// 0 is a valid value - we can't distinguish "not provided" from 0 with primitives
	return pgtype.Int8{Int64: int64(v), Valid: true}
}

func int64ToPgInt8(v int64) pgtype.Int8 {
	// 0 is a valid value - we can't distinguish "not provided" from 0 with primitives
	return pgtype.Int8{Int64: v, Valid: true}
}

func float64PtrToPgFloat4(v *float64) pgtype.Float4 {
	if v == nil {
		return pgtype.Float4{Valid: false}
	}
	return pgtype.Float4{Float32: float32(*v), Valid: true}
}

func stringPtrToPgText(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *v, Valid: true}
}

func boolPtrToPgBool(v *bool) pgtype.Bool {
	if v == nil {
		return pgtype.Bool{Valid: false}
	}
	return pgtype.Bool{Bool: *v, Valid: true}
}

// wireLicense maps the DB transcripts.license_id (NULL ⇒ legacy/un-set) onto the
// wire schema.License, in both reverse mappers (transcriptToSchema here and the
// pull mappers in pull.go). The zero value "" is dropped by the omitempty wire
// tag. Stored values are already menu-constrained (the publish 422 gate + the
// licenses FK), so no re-validation is needed on the read path.
func wireLicense(l pgtype.Text) schema.License {
	if !l.Valid {
		return ""
	}
	return schema.License(l.String)
}

func transcriptToSchema(t sqlc.Transcript) schema.PublishRequest {
	schemaVersion, _ := strconv.Atoi(t.SchemaVersion)

	return schema.PublishRequest{
		License: wireLicense(t.LicenseID),
		Identity: schema.SessionIdentity{
			SessionID:       schema.SessionID(t.LocalID),
			ParentSessionID: pgTextToSessionIDPtr(t.ParentSessionID),
			SchemaVersion:   schemaVersion,
		},
		Model: schema.ModelInfo{
			Harness:        schema.Harness(t.ModelProvider),
			Model:          schema.ModelID(t.ModelName.String),
			HarnessVersion: t.HarnessVersion.String,
		},
		Timestamp: schema.TimestampInfo{
			Start:    pgTimestamptzToInt64(t.SessionStart),
			End:      pgTimestamptzToInt64(t.SessionEnd),
			Ingested: pgTimestamptzToInt64Ptr(t.IngestedAt),
		},
		Source: schema.SourceInfo{
			FilePath: t.SourceFilePath.String,
			Format:   schema.SourceFormat(t.SourceFormat.String),
		},
		Git: schema.GitContext{
			Branch:   pgTextToStringPtr(t.GitBranch),
			Remote:   pgTextToStringPtr(t.GitRemote),
			Worktree: pgTextToStringPtr(t.GitWorktree),
		},
		Project: schema.ProjectContext{
			Hash:     schema.ProjectHash(t.ProjectHash),
			FilePath: t.ProjectPath.String,
			Name:     t.ProjectName.String,
		},
		Stats: schema.SessionStats{
			TurnCount:     pgInt4ToInt(t.TurnCount),
			ToolCallCount: pgInt4ToInt(t.ToolCallCount),
			SubagentCount: pgInt4ToInt(t.SubagentCount),
			DurationMs:    pgInt8ToInt64(t.DurationMs),
			TokensIn:      int(pgInt8ToInt64(t.TokensIn)),
			TokensOut:     int(pgInt8ToInt64(t.TokensOut)),
		},
		Quality: &schema.QualityMetrics{
			TitleGenerated:          pgTextToStringPtr(t.TitleGenerated),
			Outcome:                 pgTextToOutcomePtr(t.Outcome),
			FilesTouched:            pgInt4ToIntPtr(t.FilesTouched),
			LinesChanged:            pgInt4ToIntPtr(t.LinesChanged),
			RetryLoops:              pgInt4ToIntPtr(t.RetryLoops),
			RetryTokensWasted:       pgInt4ToIntPtr(t.RetryTokensWasted),
			WithinSessionReverts:    pgInt4ToIntPtr(t.WithinSessionReverts),
			SignalDensity:           pgFloat4ToFloat64Ptr(t.SignalDensity),
			SpecQualityScore:        pgFloat4ToFloat64Ptr(t.SpecQualityScore),
			ExplorationRatio:        pgFloat4ToFloat64Ptr(t.ExplorationRatio),
			ScopeBreadth:            pgInt4ToIntPtr(t.ScopeBreadth),
			DiscoveryTurns:          pgInt4ToIntPtr(t.DiscoveryTurns),
			M2TokenOutcomeRatio:     pgFloat4ToFloat64Ptr(t.M2TokenOutcomeRatio),
			M3UniqueToolCount:       pgInt4ToIntPtr(t.M3UniqueToolCount),
			M4ErrorRecoveryCount:    pgInt4ToIntPtr(t.M4ErrorRecoveryCount),
			M4ConsecutiveErrorMax:   pgInt4ToIntPtr(t.M4ConsecutiveErrorMax),
			M5ContextUtilizationPct: pgFloat4ToFloat64Ptr(t.M5ContextUtilizationPct),
			M5PeakContextTokens:     pgInt4ToIntPtr(t.M5PeakContextTokens),
			M5AvgMessageTokens:      pgInt4ToIntPtr(t.M5AvgMessageTokens),
			M6OutputSurvivalPct:     pgFloat4ToFloat64Ptr(t.M6OutputSurvivalPct),
			M6LinesSurvived:         pgInt4ToIntPtr(t.M6LinesSurvived),
			M6LinesTotal:            pgInt4ToIntPtr(t.M6LinesTotal),
			M7SpecWordCount:         pgInt4ToIntPtr(t.M7SpecWordCount),
			M7SpecHasExamples:       pgBoolToBoolPtr(t.M7SpecHasExamples),
			M7SpecHasConstraints:    pgBoolToBoolPtr(t.M7SpecHasConstraints),
			ComputedAt:              pgTimestamptzToInt64Ptr(t.ComputedAt),
			ComputeVersion:          pgInt4ToIntPtr(t.ComputeVersion),
		},
		Subagents: unmarshalSubagents(t.Subagents),
		Diagnostics: schema.DiagnosticsInfo{
			Warnings: unmarshalDiagnosticsWarnings(t.DiagnosticsWarnings),
			Partial:  pgBoolToBoolPtr(t.DiagnosticsPartial),
		},
	}
}

func pgTextToStringPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

func pgTextToSessionIDPtr(t pgtype.Text) *schema.SessionID {
	if !t.Valid {
		return nil
	}
	s := schema.SessionID(t.String)
	return &s
}

func pgTimestamptzToInt64(t pgtype.Timestamptz) int64 {
	if !t.Valid {
		return -1
	}
	return t.Time.UnixMilli()
}

func pgTimestamptzToInt64Ptr(t pgtype.Timestamptz) *int64 {
	if !t.Valid {
		return nil
	}
	ms := t.Time.UnixMilli()
	return &ms
}

func pgInt4ToInt(t pgtype.Int4) int {
	if !t.Valid {
		return 0
	}
	return int(t.Int32)
}

func pgInt4ToIntPtr(t pgtype.Int4) *int {
	if !t.Valid {
		return nil
	}
	v := int(t.Int32)
	return &v
}

func pgInt8ToInt64(t pgtype.Int8) int64 {
	if !t.Valid {
		return 0
	}
	return t.Int64
}

func pgFloat4ToFloat64Ptr(t pgtype.Float4) *float64 {
	if !t.Valid {
		return nil
	}
	v := float64(t.Float32)
	return &v
}

func pgBoolToBoolPtr(t pgtype.Bool) *bool {
	if !t.Valid {
		return nil
	}
	return &t.Bool
}

func pgTextToOutcomePtr(t pgtype.Text) *schema.SessionOutcome {
	if !t.Valid {
		return nil
	}
	o := schema.SessionOutcome(t.String)
	return &o
}

func unmarshalSubagents(b []byte) []schema.SubagentRef {
	if len(b) == 0 {
		return nil
	}
	var s []schema.SubagentRef
	_ = json.Unmarshal(b, &s)
	return s
}

func unmarshalDiagnosticsWarnings(b []byte) []schema.DiagnosticEntry {
	if len(b) == 0 {
		return nil
	}
	var w []schema.DiagnosticEntry
	_ = json.Unmarshal(b, &w)
	return w
}
