package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/scanner"
	"github.com/peasant-labs/village/backend/internal/storage"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
)

const transcriptSelectColumns = `t.id, t.owner_id, t.local_id, t.title, t.description, t.visibility,
	t.model_provider, t.model_name, t.harness_version, t.session_start, t.session_end,
	t.turn_count, t.token_count, t.blob_key, t.blob_size_bytes, t.schema_version,
	t.published_at, t.updated_at, t.parent_session_id, t.ingested_at, t.source_file_path,
	t.source_format, t.git_branch, t.git_remote, t.git_worktree, t.project_hash,
	t.project_path, t.project_name, t.tool_call_count, t.subagent_count, t.duration_ms,
	t.subagents, t.diagnostics_warnings, t.diagnostics_partial, t.tokens_in, t.tokens_out,
	t.title_generated, t.outcome, t.files_touched, t.lines_changed, t.retry_loops,
	t.retry_tokens_wasted, t.within_session_reverts, t.signal_density, t.spec_quality_score,
	t.exploration_ratio, t.scope_breadth, t.discovery_turns, t.m2_token_outcome_ratio,
	t.m3_unique_tool_count, t.m4_error_recovery_count, t.m4_consecutive_error_max,
	t.m5_context_utilization_pct, t.m5_peak_context_tokens, t.m5_avg_message_tokens,
	t.m6_output_survival_pct, t.m6_lines_survived, t.m6_lines_total, t.m7_spec_word_count,
	t.m7_spec_has_examples, t.m7_spec_has_constraints, t.computed_at, t.compute_version,
	t.content_hash, t.license_id, t.wrapped_data_key, t.encryption_algorithm, t.key_version,
	t.accepted_request_operation_fingerprint`

// publishRequest is the v2 nested metadata schema from the local transcript store.
type publishRequest struct {
	SessionID    string   `json:"sessionId"`
	ParentUUID   string   `json:"parentUuid"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Visibility   string   `json:"visibility"`
	Tags         []string `json:"tags"`
	ModelHarness string   `json:"modelHarness"`
	Model        string   `json:"model"`
	Version      string   `json:"version"`
	SchemaVer    int      `json:"schemaVersion"`

	Timestamp struct {
		Start    *time.Time `json:"start"`
		End      *time.Time `json:"end"`
		Ingested *int64     `json:"ingested"`
	} `json:"timestamp"`

	TurnCount  *int32 `json:"turnCount"`
	TokenCount *int32 `json:"tokenCount"`

	Source struct {
		FilePath string `json:"filePath"`
		Format   string `json:"format"`
	} `json:"source"`

	Git struct {
		Branch   string `json:"branch"`
		Remote   string `json:"remote"`
		Worktree string `json:"worktree"`
	} `json:"git"`

	Project struct {
		Hash     string `json:"hash"`
		FilePath string `json:"filePath"`
		Name     string `json:"name"`
	} `json:"project"`

	Stats struct {
		ToolCallCount           *int     `json:"toolCallCount"`
		SubagentCount           *int     `json:"subagentCount"`
		DurationMs              *int64   `json:"durationMs"`
		TokensIn                *int64   `json:"tokensIn"`
		TokensOut               *int64   `json:"tokensOut"`
		TitleGenerated          *string  `json:"titleGenerated"`
		Outcome                 *string  `json:"outcome"`
		FilesTouched            *int     `json:"filesTouched"`
		LinesChanged            *int     `json:"linesChanged"`
		RetryLoops              *int     `json:"retryLoops"`
		RetryTokensWasted       *int     `json:"retryTokensWasted"`
		WithinSessionReverts    *int     `json:"withinSessionReverts"`
		SignalDensity           *float64 `json:"signalDensity"`
		SpecQualityScore        *float64 `json:"specQualityScore"`
		ExplorationRatio        *float64 `json:"explorationRatio"`
		ScopeBreadth            *int     `json:"scopeBreadth"`
		DiscoveryTurns          *int     `json:"discoveryTurns"`
		M2TokenOutcomeRatio     *float64 `json:"m2TokenOutcomeRatio"`
		M3UniqueToolCount       *int     `json:"m3UniqueToolCount"`
		M4ErrorRecoveryCount    *int     `json:"m4ErrorRecoveryCount"`
		M4ConsecutiveErrorMax   *int     `json:"m4ConsecutiveErrorMax"`
		M5ContextUtilizationPct *float64 `json:"m5ContextUtilizationPct"`
		M5PeakContextTokens     *int     `json:"m5PeakContextTokens"`
		M5AvgMessageTokens      *int     `json:"m5AvgMessageTokens"`
		M6OutputSurvivalPct     *float64 `json:"m6OutputSurvivalPct"`
		M6LinesSurvived         *int     `json:"m6LinesSurvived"`
		M6LinesTotal            *int     `json:"m6LinesTotal"`
		M7SpecWordCount         *int     `json:"m7SpecWordCount"`
		M7SpecHasExamples       *bool    `json:"m7SpecHasExamples"`
		M7SpecHasConstraints    *bool    `json:"m7SpecHasConstraints"`
		ComputedAt              *int64   `json:"computedAt"`
		ComputeVersion          *int     `json:"computeVersion"`
	} `json:"stats"`

	Subagents   json.RawMessage `json:"subagents"`
	Diagnostics struct {
		Warnings json.RawMessage `json:"warnings"`
		Partial  *bool           `json:"partial"`
	} `json:"diagnostics"`
}

type stagedObjectCleanupError struct {
	key        string
	saveErr    error
	cleanupErr error
}

func (err *stagedObjectCleanupError) Error() string {
	return fmt.Sprintf("save transcript while staging replacement object %q: %v; deleting only the uncommitted staged object also failed: %v; the prior database row and object remain current and readable, and an operator may remove the orphaned staged object", err.key, err.saveErr, err.cleanupErr)
}

func (err *stagedObjectCleanupError) Unwrap() []error {
	return []error{err.saveErr, err.cleanupErr}
}

func publishSaveErrorMessage(err error, exposeStagedObjectKey bool) string {
	var cleanupErr *stagedObjectCleanupError
	if errors.As(err, &cleanupErr) {
		if !exposeStagedObjectKey {
			return "Failed to save transcript: staged-object cleanup evidence was logged; operator cleanup and a publish retry are required"
		}
		return "Failed to save transcript: " + cleanupErr.Error()
	}
	return "Failed to save transcript"
}

func (h *Handler) PublishTranscript(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid multipart form")
		return
	}

	metadataStr := r.FormValue("metadata")
	if metadataStr == "" {
		writeError(w, http.StatusBadRequest, "Missing metadata field")
		return
	}

	// Migrate the metadata wire surface: a transitional CLI may still
	// emit the legacy model.modelHarness / model.provider key — normalize it to
	// model.harness (key + value) before decode + enforcement so both surfaces
	// (this metadata field and the TranscriptContent envelope) speak harness.
	metaBytes := normalizeMetadataHarnessKey([]byte(metadataStr))
	var metadataObject map[string]json.RawMessage
	if err := json.Unmarshal(metaBytes, &metadataObject); err != nil || metadataObject == nil {
		writeError(w, http.StatusBadRequest, "Invalid metadata JSON")
		return
	}
	if identity, ok := metadataObject["identity"]; ok {
		var identityObject map[string]json.RawMessage
		if json.Unmarshal(identity, &identityObject) == nil {
			var sessionID string
			_ = json.Unmarshal(identityObject["sessionId"], &sessionID)
			if sessionID == "" {
				writeError(w, http.StatusBadRequest, "sessionId is required")
				return
			}
		}
	}

	authoritativeReq, authoritativeErr := schema.DecodeAuthoritativePublishRequest(metaBytes)
	legacyErr := schema.ValidatePublishRequest(metaBytes)
	authoritative := authoritativeErr == nil
	if !authoritative && legacyErr != nil {
		writeError(w, http.StatusUnprocessableEntity, "metadata failed schema validation: authoritative: "+authoritativeErr.Error()+"; legacy compatibility: "+legacyErr.Error())
		return
	}
	var req schema.PublishRequest
	if authoritative {
		projected, err := json.Marshal(authoritativeReq)
		if err != nil || json.Unmarshal(projected, &req) != nil {
			writeError(w, http.StatusInternalServerError, "Village validated the authoritative metadata but could not map it to persistence; retry after upgrading Village")
			return
		}
		// The authoritative successor field is parentSessionId, while the legacy
		// compatibility projection intentionally retains parentUuid. Materialize
		// the authoritative value explicitly instead of conflating the two wires.
		req.Identity.ParentSessionID = authoritativeReq.Identity.ParentSessionID
	} else if err := json.Unmarshal(metaBytes, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid legacy metadata JSON")
		return
	}

	// Validate the required identity + model fields with accurate, field-level
	// errors (fixes A1: the prior block validated Model.Provider but reported
	// "modelHarness", and never validated Model.Model at all).
	if req.Identity.SessionID == "" {
		writeError(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	// Enforce the vendored OpenAPI PublishRequest schema as the SOLE, fail-closed
	// gate for the required model fields (1e8tk): SchemaModelInfo.required now
	// declares [harness, model] and SchemaPublishRequest.required declares [model],
	// so an absent/empty harness or model is rejected here as a documented
	// schema-422. The prior hand-written "model is required" 400 guard is removed —
	// it enforced a rule the published spec did not declare (spec drift); an absent
	// model now unifies to 422 through the schema. FAIL-CLOSED: a nil validator
	// means the vendored schema failed to compile (a build/asset bug); reject
	// rather than silently accept an unvalidated publish.
	v := payloadValidator()
	if v == nil {
		writeError(w, http.StatusServiceUnavailable, "publish validation unavailable")
		return
	}
	if !authoritative {
		if err := v.ValidatePublish(metaBytes); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "metadata failed schema validation: "+err.Error())
			return
		}
	}
	h.sanitizeGeneratedTitle(&req)

	schemaVersion := strconv.Itoa(req.Identity.SchemaVersion)
	if schemaVersion == "0" {
		schemaVersion = "2"
	}

	file, _, err := r.FormFile("transcript_file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Missing transcript_file")
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read file")
		return
	}
	if err := requireSupportedContentCapabilityWithEvaluator(content, h.preservationProof()); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	if issues := h.scanTranscriptContent(content); len(issues) > 0 {
		writeError(w, http.StatusUnprocessableEntity, scanner.FormatScanErrors(issues))
		return
	}
	servedHash := schema.ComputeTranscriptContentHash(content)
	var fingerprint schema.PublishRequestFingerprint
	if authoritative {
		if servedHash != authoritativeReq.ContentHash {
			writeError(w, http.StatusUnprocessableEntity, "transcript content hash does not match transcript_file; recompute contentHash from the exact uploaded bytes and retry")
			return
		}
		operation, err := schema.CanonicalizePublishRequest(authoritativeReq)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "metadata failed schema validation: "+err.Error())
			return
		}
		fingerprint, err = schema.FingerprintPublishOperation(operation)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "metadata failed schema validation: "+err.Error())
			return
		}
	}

	ownerPgID := user.PgID()
	var transcript sqlc.Transcript
	var appliedAssociations []schema.PublishedAssociation
	var isUpdate bool
	var candidate storage.BlobDescriptor
	var candidateID pgtype.UUID
	var candidateWritten bool
	var superseded storage.BlobDescriptor
	var hasSuperseded bool
	var deleteSuperseded bool
	responseWritten := false
	err = h.withPublishLocks(r.Context(), ownerPgID, string(req.Identity.SessionID), req.Git.Associations, func(conn *pgxpool.Conn) error {
		lockedQueries := h.queries
		if conn != nil {
			lockedQueries = sqlc.New(conn)
		}
		existingID, probeErr := lockedQueries.GetTranscriptIDByOwnerAndLocalID(r.Context(), sqlc.GetTranscriptIDByOwnerAndLocalIDParams{
			OwnerID: ownerPgID,
			LocalID: string(req.Identity.SessionID),
		})

		// Publish identity is SOURCE-keyed: the probe above matched on (owner, the
		// client's local session id), so re-publishing the same local id reuses the
		// existing row (update) while a different local id is always a new row. The
		// transcript CONTENT is never consulted for identity: two byte-identical
		// payloads under different local ids are distinct rows (forks), and content_hash
		// is a plain value-only column, never an idempotency key.
		isUpdate = probeErr == nil
		if conn != nil && probeErr != nil && !errors.Is(probeErr, pgx.ErrNoRows) {
			return fmt.Errorf("probe transcript source identity: %w", probeErr)
		}
		var transcriptID pgtype.UUID
		operation := "create"
		var persistence transactionResult
		if isUpdate {
			operation = "republish"
			transcriptID = existingID
		} else {
			transcriptID = toPgUUID(uuid.New())
		}

		// Validate association conflicts while their narrow advisory locks are held
		// and BEFORE replacing the canonical blob. The final DB transaction repeats
		// this check as the constraint-backed mutation boundary; the session locks make
		// concurrent application publishes observe the same canonical binding.
		if len(req.Git.Associations) > 0 {
			preflightTranscriptID := pgtype.UUID{}
			if isUpdate {
				preflightTranscriptID = existingID
			}
			if err := h.inTxAsOnConn(r.Context(), conn, ownerPgID, func(q Querier) error {
				_, err := validatePublishedAssociationBindings(r.Context(), q, ownerPgID, preflightTranscriptID, req.Git.Associations)
				return err
			}); err != nil {
				if errors.Is(err, ErrAssociationBinding) {
					writeError(w, http.StatusUnprocessableEntity, err.Error())
					responseWritten = true
					return nil
				}
				return fmt.Errorf("validate association publish: %w", err)
			}
		}

		if h.blobs == nil {
			writeError(w, http.StatusServiceUnavailable, "Encrypted transcript storage is unavailable because the blob store was not composed before handler.PublishTranscript; no transcript was written; configure key custody and object storage, then restart and retry")
			responseWritten = true
			return nil
		}
		if isUpdate {
			current, currentErr := lockedQueries.GetTranscriptByID(r.Context(), transcriptID)
			if currentErr != nil {
				return fmt.Errorf("republish descriptor load failed before candidate write: %w", currentErr)
			}
			superseded, currentErr = descriptorFromTranscript(current)
			if currentErr != nil {
				return currentErr
			}
			hasSuperseded = true
			if authoritative && current.AcceptedRequestOperationFingerprint.Valid && current.AcceptedRequestOperationFingerprint.String == fingerprint.String() {
				appliedAssociations, currentErr = readPublishedAssociations(r.Context(), lockedQueries, transcriptID)
				if currentErr != nil {
					return currentErr
				}
				transcript = current
				return nil
			}
			if current.Visibility != dbVisibilityPrivate {
				private := dbVisibilityPrivate
				if currentErr := h.inTxAsOnConn(r.Context(), conn, ownerPgID, func(q Querier) error {
					_, err := applyMetadataPatch(r.Context(), q, existingID, metadataPatch{Visibility: &private})
					return err
				}); currentErr != nil {
					return fmt.Errorf("narrow transcript before encrypted content replacement: %w", currentErr)
				}
			}
		}
		descriptor, identity, err := h.blobs.Write(r.Context(), uuid.UUID(transcriptID.Bytes), content)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Encrypted transcript write failed before database persistence; no transcript metadata was committed; verify key custody and object storage, then retry")
			responseWritten = true
			return nil
		}
		candidate, candidateID, candidateWritten = descriptor, transcriptID, true
		blobKey := string(descriptor.ObjectKey())

		blobSize := int64(len(content))

		// Use mapper to convert schema.PublishRequest to DB params
		params := schemaToTranscriptParams(req, blobKey, blobSize, schemaVersion)
		params.ID = transcriptID
		params.OwnerID = ownerPgID
		params.LocalID = string(req.Identity.SessionID)

		// Normalize nil JSONB to null
		if len(params.Subagents) == 0 {
			params.Subagents = nil
		}
		if len(params.DiagnosticsWarnings) == 0 {
			params.DiagnosticsWarnings = nil
		}

		if isUpdate {
			republishParams := sqlc.UpdateTranscriptByOwnerAndLocalIDParams{
				OwnerID:                 ownerPgID,
				LocalID:                 string(req.Identity.SessionID),
				Title:                   params.Title,
				Description:             params.Description,
				Visibility:              params.Visibility,
				ModelProvider:           params.ModelProvider,
				ModelName:               params.ModelName,
				HarnessVersion:          params.HarnessVersion,
				SessionStart:            params.SessionStart,
				SessionEnd:              params.SessionEnd,
				TurnCount:               params.TurnCount,
				TokenCount:              params.TokenCount,
				BlobKey:                 params.BlobKey,
				BlobSizeBytes:           params.BlobSizeBytes,
				SchemaVersion:           params.SchemaVersion,
				ParentSessionID:         params.ParentSessionID,
				IngestedAt:              params.IngestedAt,
				SourceFilePath:          params.SourceFilePath,
				SourceFormat:            params.SourceFormat,
				GitBranch:               params.GitBranch,
				GitRemote:               params.GitRemote,
				GitWorktree:             params.GitWorktree,
				ProjectHash:             params.ProjectHash,
				ProjectPath:             params.ProjectPath,
				ProjectName:             params.ProjectName,
				ToolCallCount:           params.ToolCallCount,
				SubagentCount:           params.SubagentCount,
				DurationMs:              params.DurationMs,
				Subagents:               params.Subagents,
				DiagnosticsWarnings:     params.DiagnosticsWarnings,
				DiagnosticsPartial:      params.DiagnosticsPartial,
				TokensIn:                params.TokensIn,
				TokensOut:               params.TokensOut,
				TitleGenerated:          params.TitleGenerated,
				Outcome:                 params.Outcome,
				FilesTouched:            params.FilesTouched,
				LinesChanged:            params.LinesChanged,
				RetryLoops:              params.RetryLoops,
				RetryTokensWasted:       params.RetryTokensWasted,
				WithinSessionReverts:    params.WithinSessionReverts,
				SignalDensity:           params.SignalDensity,
				SpecQualityScore:        params.SpecQualityScore,
				ExplorationRatio:        params.ExplorationRatio,
				ScopeBreadth:            params.ScopeBreadth,
				DiscoveryTurns:          params.DiscoveryTurns,
				M2TokenOutcomeRatio:     params.M2TokenOutcomeRatio,
				M3UniqueToolCount:       params.M3UniqueToolCount,
				M4ErrorRecoveryCount:    params.M4ErrorRecoveryCount,
				M4ConsecutiveErrorMax:   params.M4ConsecutiveErrorMax,
				M5ContextUtilizationPct: params.M5ContextUtilizationPct,
				M5PeakContextTokens:     params.M5PeakContextTokens,
				M5AvgMessageTokens:      params.M5AvgMessageTokens,
				M6OutputSurvivalPct:     params.M6OutputSurvivalPct,
				M6LinesSurvived:         params.M6LinesSurvived,
				M6LinesTotal:            params.M6LinesTotal,
				M7SpecWordCount:         params.M7SpecWordCount,
				M7SpecHasExamples:       params.M7SpecHasExamples,
				M7SpecHasConstraints:    params.M7SpecHasConstraints,
				ComputedAt:              params.ComputedAt,
				ComputeVersion:          params.ComputeVersion,
				LicenseID:               params.LicenseID,
				ContentHash:             pgtype.Text{String: string(identity.Hash()), Valid: true},
				WrappedDataKey:          descriptor.WrappedDEK(),
				EncryptionAlgorithm:     string(descriptor.Algorithm()),
				KeyVersion:              int32(descriptor.KeyVersion()),
			}
			// One txn, actor = the publisher: pin the governance axes from the LOCKED
			// narrow pre-image (visibility never changes on re-publish; an absent CLI
			// license preserves the current one), then update. The migration-026
			// trigger records license_changed iff the license actually moved.
			persistence = h.inEncryptedTxAsOnConn(r.Context(), conn, ownerPgID, func(q Querier) error {
				if pinErr := pinRepublishGovernance(r.Context(), q, existingID, &republishParams); pinErr != nil {
					return pinErr
				}
				newAssociations, associationErr := validatePublishedAssociationBindings(r.Context(), q, ownerPgID, existingID, req.Git.Associations)
				if associationErr != nil {
					return associationErr
				}
				var txErr error
				transcript, txErr = q.UpdateTranscriptByOwnerAndLocalID(r.Context(), republishParams)
				if txErr != nil {
					return txErr
				}
				if err := insertPublishedAssociationBindings(r.Context(), q, ownerPgID, transcript.ID, newAssociations); err != nil {
					return err
				}
				if authoritative {
					return finalizeAuthoritativePublish(r.Context(), q, transcript.ID, servedHash, fingerprint, req.Git.Commits, &appliedAssociations)
				}
				if err := q.SetAcceptedRequestOperationFingerprint(r.Context(), sqlc.SetAcceptedRequestOperationFingerprintParams{ID: transcript.ID, AcceptedRequestOperationFingerprint: pgtype.Text{Valid: false}}); err != nil {
					return fmt.Errorf("clear authoritative fingerprint during legacy republish: %w", err)
				}
				return persistCommits(r.Context(), q, transcript.ID, req.Git.Commits)
			})
		} else {
			createParams := sqlc.CreateTranscriptParams{
				ID:                      transcriptID,
				OwnerID:                 ownerPgID,
				LocalID:                 string(req.Identity.SessionID),
				Title:                   params.Title,
				Description:             params.Description,
				Visibility:              params.Visibility,
				ModelProvider:           params.ModelProvider,
				ModelName:               params.ModelName,
				HarnessVersion:          params.HarnessVersion,
				SessionStart:            params.SessionStart,
				SessionEnd:              params.SessionEnd,
				TurnCount:               params.TurnCount,
				TokenCount:              params.TokenCount,
				BlobKey:                 params.BlobKey,
				BlobSizeBytes:           params.BlobSizeBytes,
				SchemaVersion:           params.SchemaVersion,
				ParentSessionID:         params.ParentSessionID,
				IngestedAt:              params.IngestedAt,
				SourceFilePath:          params.SourceFilePath,
				SourceFormat:            params.SourceFormat,
				GitBranch:               params.GitBranch,
				GitRemote:               params.GitRemote,
				GitWorktree:             params.GitWorktree,
				ProjectHash:             params.ProjectHash,
				ProjectPath:             params.ProjectPath,
				ProjectName:             params.ProjectName,
				ToolCallCount:           params.ToolCallCount,
				SubagentCount:           params.SubagentCount,
				DurationMs:              params.DurationMs,
				Subagents:               params.Subagents,
				DiagnosticsWarnings:     params.DiagnosticsWarnings,
				DiagnosticsPartial:      params.DiagnosticsPartial,
				TokensIn:                params.TokensIn,
				TokensOut:               params.TokensOut,
				TitleGenerated:          params.TitleGenerated,
				Outcome:                 params.Outcome,
				FilesTouched:            params.FilesTouched,
				LinesChanged:            params.LinesChanged,
				RetryLoops:              params.RetryLoops,
				RetryTokensWasted:       params.RetryTokensWasted,
				WithinSessionReverts:    params.WithinSessionReverts,
				SignalDensity:           params.SignalDensity,
				SpecQualityScore:        params.SpecQualityScore,
				ExplorationRatio:        params.ExplorationRatio,
				ScopeBreadth:            params.ScopeBreadth,
				DiscoveryTurns:          params.DiscoveryTurns,
				M2TokenOutcomeRatio:     params.M2TokenOutcomeRatio,
				M3UniqueToolCount:       params.M3UniqueToolCount,
				M4ErrorRecoveryCount:    params.M4ErrorRecoveryCount,
				M4ConsecutiveErrorMax:   params.M4ConsecutiveErrorMax,
				M5ContextUtilizationPct: params.M5ContextUtilizationPct,
				M5PeakContextTokens:     params.M5PeakContextTokens,
				M5AvgMessageTokens:      params.M5AvgMessageTokens,
				M6OutputSurvivalPct:     params.M6OutputSurvivalPct,
				M6LinesSurvived:         params.M6LinesSurvived,
				M6LinesTotal:            params.M6LinesTotal,
				M7SpecWordCount:         params.M7SpecWordCount,
				M7SpecHasExamples:       params.M7SpecHasExamples,
				M7SpecHasConstraints:    params.M7SpecHasConstraints,
				ComputedAt:              params.ComputedAt,
				ComputeVersion:          params.ComputeVersion,
				LicenseID:               params.LicenseID,
				ContentHash:             pgtype.Text{String: string(identity.Hash()), Valid: true},
				WrappedDataKey:          descriptor.WrappedDEK(),
				EncryptionAlgorithm:     string(descriptor.Algorithm()),
				KeyVersion:              int32(descriptor.KeyVersion()),
			}
			// One txn, actor = the publisher; the migration-026 AFTER INSERT trigger
			// appends the 'published' snapshot — there is no application audit writer.
			persistence = h.inEncryptedTxAsOnConn(r.Context(), conn, ownerPgID, func(q Querier) error {
				newAssociations, associationErr := validatePublishedAssociationBindings(r.Context(), q, ownerPgID, pgtype.UUID{}, req.Git.Associations)
				if associationErr != nil {
					return associationErr
				}
				var txErr error
				transcript, txErr = q.CreateTranscript(r.Context(), createParams)
				if txErr != nil {
					return txErr
				}
				if err := insertPublishedAssociationBindings(r.Context(), q, ownerPgID, transcript.ID, newAssociations); err != nil {
					return err
				}
				if authoritative {
					return finalizeAuthoritativePublish(r.Context(), q, transcript.ID, servedHash, fingerprint, req.Git.Commits, &appliedAssociations)
				}
				return persistCommits(r.Context(), q, transcript.ID, req.Git.Commits)
			})
		}
		err = persistence.Err
		decision := cleanupDecision(operation, persistence.Completion, persistence.Err == nil)
		if decision.DeleteCandidate && candidateWritten {
			cleanupOperation := cleanupCreateCandidate
			if isUpdate {
				cleanupOperation = cleanupRepublishCandidate
			}
			if cleanupErr := h.deleteBlobForCleanup(r.Context(), cleanupOperation, candidateID, candidate, persistence.Completion); cleanupErr != nil {
				err = &stagedObjectCleanupError{key: string(candidate.ObjectKey()), saveErr: persistence.Err, cleanupErr: cleanupErr}
			}
			candidateWritten = false
		} else if decision.Reconcile && candidateWritten {
			emitBlobReconciliation(operation, candidateID, candidate, persistence.Completion)
			candidateWritten = false
		}
		deleteSuperseded = decision.DeleteSuperseded
		if err != nil {
			if errors.Is(err, ErrAssociationBinding) {
				writeError(w, http.StatusUnprocessableEntity, err.Error())
				responseWritten = true
				return nil
			}
			return fmt.Errorf("save encrypted transcript and publication state atomically: %w", err)
		}

		return nil
	})
	if err != nil {
		if candidateWritten {
			emitBlobReconciliation("publish", candidateID, candidate, TransactionCommitAmbiguous)
		}
		writeError(w, http.StatusInternalServerError, publishSaveErrorMessage(err, authoritative))
		return
	}
	if responseWritten {
		return
	}
	if hasSuperseded && deleteSuperseded {
		_ = h.deleteBlobForCleanup(r.Context(), cleanupRepublishSuperseded, transcript.ID, superseded, TransactionCommitted)
	}

	// Note: Tags are not part of schema.PublishRequest in the new wire format
	// Tags linking is deferred to a future enhancement

	status := http.StatusCreated
	if isUpdate {
		status = http.StatusOK
	}
	if !authoritative {
		tags, _ := h.queries.GetTranscriptTags(r.Context(), transcript.ID)
		writeJSON(w, status, map[string]any{"transcript": publishTranscriptResponse(transcript), "tags": tags})
		return
	}
	transcriptID, err := schema.NewTranscriptID(uuid.UUID(transcript.ID.Bytes).String())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to construct authoritative publish response")
		return
	}
	response := schema.AuthoritativePublishResponse{
		TranscriptID:                transcriptID,
		TranscriptURL:               transcriptFrontendURL(h.cfg.FrontendURL, transcriptID),
		Visibility:                  schemaVisibility(transcript.Visibility),
		ContentHash:                 servedHash,
		RequestOperationFingerprint: fingerprint,
		Applied: schema.PublishAppliedState{
			License:          pgLicense(transcript.LicenseID),
			Associations:     appliedAssociations,
			NormalizedValues: schema.PublishNormalizedValues{RootHarness: schema.Harness(transcript.ModelProvider), EntryHarnesses: publishedEntryHarnesses(req.Entries), DerivedTitle: pgTextPointer(transcript.Title), Visibility: schemaVisibility(transcript.Visibility), SchemaVersion: transcript.SchemaVersion},
		},
		BlobKey: transcript.BlobKey, BlobSizeBytes: transcript.BlobSizeBytes.Int64,
		PublishedAt: transcript.PublishedAt.Time.UnixMilli(), UpdatedAt: transcript.UpdatedAt.Time.UnixMilli(), Created: !isUpdate,
	}
	if err := response.Validate(); err != nil {
		writeError(w, http.StatusInternalServerError, "Village persisted the publication but could not construct a complete authoritative receipt; retry the publish to read the accepted state")
		return
	}
	writeJSON(w, status, response)
}

func finalizeAuthoritativePublish(ctx context.Context, q Querier, transcriptID pgtype.UUID, contentHash schema.TranscriptContentHash, fingerprint schema.PublishRequestFingerprint, commits []schema.CommitInfo, associations *[]schema.PublishedAssociation) error {
	if err := q.SetTranscriptContentHash(ctx, sqlc.SetTranscriptContentHashParams{ID: transcriptID, ContentHash: pgtype.Text{String: contentHash.String(), Valid: true}}); err != nil {
		return fmt.Errorf("persist authoritative content hash: %w", err)
	}
	if err := q.SetAcceptedRequestOperationFingerprint(ctx, sqlc.SetAcceptedRequestOperationFingerprintParams{ID: transcriptID, AcceptedRequestOperationFingerprint: pgtype.Text{String: fingerprint.String(), Valid: true}}); err != nil {
		return fmt.Errorf("persist accepted publish fingerprint: %w", err)
	}
	if err := persistCommits(ctx, q, transcriptID, commits); err != nil {
		return fmt.Errorf("persist authoritative commit set: %w", err)
	}
	rows, err := readPublishedAssociations(ctx, q, transcriptID)
	if err != nil {
		return err
	}
	*associations = rows
	return nil
}

func readPublishedAssociations(ctx context.Context, q Querier, transcriptID pgtype.UUID) ([]schema.PublishedAssociation, error) {
	rows, err := q.ListTranscriptAssociationsByTranscript(ctx, transcriptID)
	if err != nil {
		return nil, fmt.Errorf("read complete authoritative association set: %w", err)
	}
	result := make([]schema.PublishedAssociation, 0, len(rows))
	for _, row := range rows {
		id, err := schema.NewAssociationID(row.AssociationID)
		if err != nil {
			return nil, fmt.Errorf("read stored association identity: %w", err)
		}
		result = append(result, schema.PublishedAssociation{ID: id, ObservedCommitHash: row.ObservedCommitSha})
	}
	return result, nil
}

func transcriptFrontendURL(base string, id schema.TranscriptID) string {
	return strings.TrimRight(base, "/") + "/transcripts/" + url.PathEscape(id.String())
}

func pgLicense(value pgtype.Text) *schema.License {
	if !value.Valid {
		return nil
	}
	license := schema.License(value.String)
	return &license
}

func pgTextPointer(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func schemaVisibility(value string) schema.Visibility {
	if value == dbVisibilityShared {
		return schema.VisibilityGroup
	}
	return schema.Visibility(value)
}

func publishedEntryHarnesses(entries []schema.SessionEntry) []schema.Harness {
	seen := map[schema.Harness]struct{}{}
	result := make([]schema.Harness, 0)
	for _, entry := range entries {
		if _, ok := seen[entry.Harness]; ok {
			continue
		}
		seen[entry.Harness] = struct{}{}
		result = append(result, entry.Harness)
	}
	return result
}

func (h *Handler) PublishBatch(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "Batch publish coming soon")
}

func (h *Handler) GetTranscript(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}

	transcript, err := h.queries.GetTranscriptByID(r.Context(), toPgUUID(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	user := GetUser(r.Context())
	if !h.canViewTranscript(r.Context(), user, transcript) {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	tags, _ := h.queries.GetTranscriptTags(r.Context(), transcript.ID)
	shares, _ := h.queries.ListTranscriptShares(r.Context(), transcript.ID)
	owner, _ := h.queries.GetUserByID(r.Context(), transcript.OwnerID)
	ownerOrgs, _ := h.queries.ListUserVisibleOrgs(r.Context(), transcript.OwnerID)
	attestations, _ := h.queries.ListTranscriptAttestations(r.Context(), transcript.ID)

	// Enrich shares with acceptance_mode
	enrichedShares, _ := h.queries.ListSharesByTranscriptIDs(r.Context(), []pgtype.UUID{transcript.ID})

	writeJSON(w, http.StatusOK, map[string]any{
		"transcript":      detailTranscriptResponse(transcript),
		"tags":            tags,
		"shares":          shares,
		"enriched_shares": enrichedShares,
		"owner":           owner,
		"owner_orgs":      ownerOrgs,
		"attestations":    attestations,
	})
}

func (h *Handler) GetTranscriptContent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}

	transcript, err := h.queries.GetTranscriptByID(r.Context(), toPgUUID(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	user := GetUser(r.Context())
	if !h.canViewTranscript(r.Context(), user, transcript) {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	readResult, err := h.readEncryptedTranscript(r.Context(), transcript, "", func(fresh sqlc.Transcript) bool {
		return h.canViewTranscript(r.Context(), user, fresh)
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	raw := readResult.Plaintext

	// Migrate-on-read: normalize legacy/older decrypted transcript content to the
	// current SessionDetailPayload shape and serve the bare payload the viewer
	// expects (unwrapping the TranscriptContent envelope that peasant uploads).
	payload, rewrite, err := defaultContentMigrator.Migrate(r.Context(), raw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := validateObservedModelValues(payload); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rewrite {
		canonical, marshalErr := encodeCanonicalTranscript(payload)
		if marshalErr != nil {
			if payloadCarriesObservedModels(payload) {
				writeError(w, http.StatusInternalServerError, marshalErr.Error())
				return
			}
			log.Printf("canonical_transcript_rewrite_retryable transcript_id=%s stage=encode error=%v", uuidFromPg(readResult.Row.ID), marshalErr)
			writeJSON(w, http.StatusOK, payload)
			return
		}
		if err := h.rewriteCanonicalTranscript(r.Context(), readResult.Row, canonical); err != nil {
			if payloadCarriesObservedModels(payload) {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			log.Printf("canonical_transcript_rewrite_retryable transcript_id=%s stage=persist error=%v", uuidFromPg(readResult.Row.ID), err)
		}
	}

	writeJSON(w, http.StatusOK, payload)
}

func (h *Handler) rewriteCanonicalTranscript(ctx context.Context, row sqlc.Transcript, canonical []byte) error {
	oldDescriptor, err := descriptorFromTranscript(row)
	if err != nil {
		return err
	}
	if h.blobs == nil {
		return fmt.Errorf("canonical transcript rewrite failed because encrypted blob storage was not composed in handler.rewriteCanonicalTranscript before migrate-on-read persistence; the old generation remains authoritative and the response was withheld to avoid claiming a durable preservation Village cannot complete; configure key custody and object storage, restart Village, then retry")
	}
	unlock := defaultRewriteLocks.Lock(string(oldDescriptor.ObjectKey()))
	defer unlock()
	current, err := h.queries.GetTranscriptByID(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("canonical transcript rewrite failed because the current database descriptor could not be reloaded in handler.rewriteCanonicalTranscript after migration and before immutable replacement; the old generation remains authoritative and no response was served; restore PostgreSQL access, then retry: %w", err)
	}
	currentDescriptor, err := descriptorFromTranscript(current)
	if err != nil {
		return err
	}
	if !descriptorsEqual(oldDescriptor, currentDescriptor) {
		// Another request already installed a canonical immutable generation. The
		// caller's payload came from the authenticated old generation and the
		// compare-before-write prevents it from overwriting the winner, so this is
		// a successful no-op rather than a rewrite failure.
		return nil
	}
	newDescriptor, identity, err := h.blobs.Write(ctx, uuidFromPg(row.ID), canonical)
	if err != nil {
		return fmt.Errorf("canonical transcript rewrite failed because encrypted object storage rejected the replacement in handler.rewriteCanonicalTranscript after typed migration; the old generation remains authoritative and no response was served; restore key custody and object storage, then retry: %w", err)
	}
	systemID, err := uuid.Parse(database.SystemActorID)
	if err != nil {
		return fmt.Errorf("canonical transcript rewrite failed because the reserved system actor could not be parsed in handler.rewriteCanonicalTranscript before database installation; the candidate ciphertext may require reconciliation and no response was served; correct database.SystemActorID and retry: %w", err)
	}
	result := h.inEncryptedTx(ctx, toPgUUID(systemID), func(q Querier) error {
		_, err := q.CompareAndSwapTranscriptBlob(ctx, sqlc.CompareAndSwapTranscriptBlobParams{
			BlobKey: string(newDescriptor.ObjectKey()), WrappedDataKey: newDescriptor.WrappedDEK(), EncryptionAlgorithm: string(newDescriptor.Algorithm()), KeyVersion: int32(newDescriptor.KeyVersion()),
			ContentHash: pgtype.Text{String: string(identity.Hash()), Valid: true}, PlaintextSize: pgtype.Int8{Int64: identity.PlaintextSize(), Valid: true}, ID: row.ID,
			ExpectedBlobKey: string(oldDescriptor.ObjectKey()), ExpectedWrappedDataKey: oldDescriptor.WrappedDEK(), ExpectedEncryptionAlgorithm: string(oldDescriptor.Algorithm()), ExpectedKeyVersion: int32(oldDescriptor.KeyVersion()),
		})
		return err
	})
	decision := cleanupDecision("rewrite", result.Completion, result.Err == nil)
	if decision.DeleteSuperseded {
		_ = h.deleteBlobForCleanup(ctx, cleanupRewriteSuperseded, row.ID, oldDescriptor, result.Completion)
	} else if decision.DeleteCandidate {
		_ = h.deleteBlobForCleanup(ctx, cleanupRewriteCandidate, row.ID, newDescriptor, result.Completion)
	} else if decision.Reconcile {
		emitBlobReconciliation("rewrite", row.ID, newDescriptor, result.Completion)
	}
	if result.Err != nil {
		return fmt.Errorf("canonical transcript rewrite failed because the encrypted database transaction did not install the migrated descriptor in handler.rewriteCanonicalTranscript after candidate upload; completion=%s determines candidate cleanup and no response was served; inspect transcript_blob_reconciliation_required when completion is ambiguous, restore PostgreSQL, then retry: %w", result.Completion, result.Err)
	}
	return nil
}

func (h *Handler) UpdateTranscript(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}

	pgID := toPgUUID(id)
	transcript, err := h.queries.GetTranscriptByID(r.Context(), pgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	if transcript.OwnerID != user.PgID() {
		writeError(w, http.StatusForbidden, "Not the transcript owner")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read owner update request")
		return
	}
	// Preserve the browser's shipped empty-string spelling for an un-license
	// request while the successor contract uses explicit null for the same intent.
	var compatibilityFields map[string]json.RawMessage
	if json.Unmarshal(body, &compatibilityFields) == nil && string(compatibilityFields["license"]) == `""` {
		compatibilityFields["license"] = json.RawMessage("null")
		body, _ = json.Marshal(compatibilityFields)
	}
	req, err := schema.DecodeOwnerTranscriptUpdateRequest(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Title != nil {
		if h.titles == nil {
			writeError(w, http.StatusUnprocessableEntity, "title update rejected because PATCH title validation is unavailable before the database write; no transcript row changed; restore the title safety service and retry")
			return
		}
		result, validationErr := h.titles.Sanitize(*req.Title, redact.TitleContext{Harness: schema.Harness(transcript.ModelProvider), ProjectPath: transcript.ProjectPath.String})
		if validationErr != nil {
			writeError(w, http.StatusUnprocessableEntity, "title update rejected because PATCH title validation failed before the database write; no transcript row changed; retry after the title safety service is restored")
			return
		}
		if result.HasSensitiveContent() {
			writeError(w, http.StatusUnprocessableEntity, titleValidationMessage(result.Categories))
			return
		}
	}

	// Build the partial-update intent; the writer resolves omitted fields against the
	// LOCKED pre-image inside the txn (so a concurrent edit is not reverted). License
	// is menu-constrained: omitted ⇒ preserve, "" ⇒ clear to NULL, a menu value ⇒ set.
	// Validated here (it is not the vendored publish contract).
	patch := metadataPatch{
		Title:       req.Title,
		Description: req.Description,
		Tags:        req.Tags,
	}
	if req.Visibility != nil {
		visibility := req.Visibility.String()
		patch.Visibility = &visibility
	}
	if req.License.IsSet() {
		patch.LicenseProvided = true
		if license, ok := req.License.Value(); ok {
			patch.License = pgtype.Text{String: string(license), Valid: true}
		} else {
			patch.License = pgtype.Text{Valid: false}
		}
	}

	// One txn, actor = the editor. The migration-026 UPDATE trigger writes
	// license_changed / visibility_changed / governance_changed iff a governance
	// axis actually moved (its WHEN clause is the no-op suppression).
	var updated sqlc.Transcript
	var tags []sqlc.Tag
	err = h.withPublishLocks(r.Context(), user.PgID(), transcript.LocalID, nil, func(conn *pgxpool.Conn) error {
		return h.inTxAsOnConn(r.Context(), conn, user.PgID(), func(q Querier) error {
			var txErr error
			updated, txErr = applyMetadataPatch(r.Context(), q, pgID, patch)
			if txErr != nil {
				return txErr
			}
			if patch.Tags != nil {
				if err := linkTagsWithQuerier(r.Context(), q, pgID, *patch.Tags); err != nil {
					return err
				}
			}
			tags, txErr = q.GetTranscriptTags(r.Context(), pgID)
			return txErr
		})
	})
	if errors.Is(err, errUnlicenseBlocked) {
		writeError(w, http.StatusBadRequest, errUnlicenseBlocked.Error())
		return
	}
	if errors.Is(err, errSharedVisibilityRequiresNarrowing) {
		writeError(w, http.StatusBadRequest, errSharedVisibilityRequiresNarrowing.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update transcript")
		return
	}

	// Note: Tags are not part of schema.PublishRequest in the new wire format

	transcriptID, err := schema.NewTranscriptID(id.String())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to construct owner update response")
		return
	}
	tagNames := make([]string, 0, len(tags))
	for _, tag := range tags {
		tagNames = append(tagNames, tag.Name)
	}
	frontendURL := "https://localhost"
	if h.cfg != nil && h.cfg.FrontendURL != "" {
		frontendURL = h.cfg.FrontendURL
	}
	response := schema.OwnerTranscriptUpdateResponse{TranscriptID: transcriptID, TranscriptURL: transcriptFrontendURL(frontendURL, transcriptID), Title: pgTextPointer(updated.Title), Description: pgTextPointer(updated.Description), Tags: tagNames, License: pgLicense(updated.LicenseID), Visibility: schema.TranscriptUpdateVisibility(updated.Visibility), UpdatedAt: updated.UpdatedAt.Time.UnixMilli()}
	if err := response.Validate(); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to validate owner update response against the pinned schema; the stored transcript was updated but its response cannot be represented, so inspect the transcript visibility before retrying")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) DeleteTranscript(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}

	pgID := toPgUUID(id)
	transcript, err := h.queries.GetTranscriptByID(r.Context(), pgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}

	if transcript.OwnerID != user.PgID() {
		writeError(w, http.StatusForbidden, "Not the transcript owner")
		return
	}

	descriptor, err := descriptorFromTranscript(transcript)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.blobs == nil {
		writeError(w, http.StatusServiceUnavailable, "Encrypted transcript deletion is unavailable because the blob store was not composed in handler.DeleteTranscript before row removal; no database state changed; configure key custody and object storage, then restart and retry")
		return
	}
	result := h.inEncryptedTx(r.Context(), user.PgID(), func(q Querier) error {
		_, deleteErr := q.DeleteTranscriptReturningDescriptor(r.Context(), pgID)
		return deleteErr
	})
	decision := cleanupDecision("delete", result.Completion, result.Err == nil)
	if !decision.DeleteTarget {
		if decision.Reconcile {
			emitBlobReconciliation("delete", transcript.ID, descriptor, result.Completion)
		}
		writeError(w, http.StatusInternalServerError, "Failed to delete transcript")
		return
	}
	if err := h.deleteBlobForCleanup(r.Context(), cleanupDeleteTarget, transcript.ID, descriptor, result.Completion); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) RenameUserProject(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())

	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	from := strings.TrimSpace(req.From)
	to := strings.TrimSpace(req.To)
	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "Both from and to project names are required")
		return
	}
	if from == to {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "updated": 0})
		return
	}
	if len(to) > 255 {
		writeError(w, http.StatusBadRequest, "Project name too long")
		return
	}

	updated, err := h.queries.RenameUserProject(r.Context(), sqlc.RenameUserProjectParams{
		OwnerID:       user.PgID(),
		ProjectName:   toPgText(from),
		ProjectName_2: toPgText(to),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to rename project")
		return
	}
	if updated == 0 {
		writeError(w, http.StatusNotFound, "Project not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "updated": updated})
}

func (h *Handler) ShareTranscript(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}
	transcript, err := h.queries.GetTranscriptByID(r.Context(), toPgUUID(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}
	if transcript.OwnerID != user.PgID() {
		writeError(w, http.StatusForbidden, "Not the transcript owner")
		return
	}
	if err := h.withPublishLocks(r.Context(), user.PgID(), transcript.LocalID, nil, func(conn *pgxpool.Conn) error {
		h.shareTranscriptLocked(w, r, conn)
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to serialize transcript sharing; retry the share operation")
	}
}

func (h *Handler) shareTranscriptLocked(w http.ResponseWriter, r *http.Request, conn *pgxpool.Conn) {
	user := GetUser(r.Context())
	q := h.queries
	if conn != nil {
		q = sqlc.New(conn)
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}

	pgID := toPgUUID(id)
	transcript, err := q.GetTranscriptByID(r.Context(), pgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}
	if transcript.OwnerID != user.PgID() {
		writeError(w, http.StatusForbidden, "Not the transcript owner")
		return
	}

	var req struct {
		GroupIDs []string `json:"group_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var alreadyShared []string
	for _, gidStr := range req.GroupIDs {
		gid, err := uuid.Parse(gidStr)
		if err != nil {
			continue
		}
		pgGID := toPgUUID(gid)

		// Check if already shared with this group
		shared, err := q.IsTranscriptSharedWithGroup(r.Context(), sqlc.IsTranscriptSharedWithGroupParams{
			TranscriptID: pgID,
			GroupID:      pgGID,
		})
		if err == nil && shared {
			alreadyShared = append(alreadyShared, gidStr)
			continue
		}

		_, err = q.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
			GroupID: pgGID,
			UserID:  user.PgID(),
		})
		if err != nil {
			continue
		}

		// Determine share status based on collective's acceptance mode
		group, err := q.GetGroupByID(r.Context(), pgGID)
		if err != nil {
			continue
		}

		status := "approved"
		switch group.AcceptanceMode {
		case "verified_only":
			// If the collective is linked to a specific GitHub org, require
			// the user to have THAT org marked visible. Otherwise fall back
			// to the legacy "any visible org" check.
			if group.LinkedGithubOrg.Valid && group.LinkedGithubOrg.String != "" {
				ok, err := q.HasUserVisibleOrg(r.Context(), sqlc.HasUserVisibleOrgParams{
					UserID: user.PgID(),
					Lower:  group.LinkedGithubOrg.String,
				})
				if err != nil || !ok {
					continue // skip — user does not have the required org visible
				}
			} else {
				visOrgs, _ := q.ListUserVisibleOrgs(r.Context(), user.PgID())
				if len(visOrgs) == 0 {
					continue // skip — user has no visible orgs
				}
			}
		case "curated":
			status = "pending"
		}

		q.ShareTranscriptWithStatus(r.Context(), sqlc.ShareTranscriptWithStatusParams{
			TranscriptID: pgID,
			GroupID:      pgGID,
			Status:       status,
		})
	}

	if len(alreadyShared) > 0 && len(alreadyShared) == len(req.GroupIDs) {
		writeError(w, http.StatusConflict, "Transcript is already shared with this collective")
		return
	}

	// Sharing a private transcript flips it to 'shared'. The flip runs under
	// inTxAs against the locked pre-image, so the migration-026 trigger records
	// visibility_changed atomically (already-shared / concurrent cases are the
	// trigger's WHEN-clause no-op); only flip FROM private, leaving an explicit
	// public/shared intact.
	if transcript.Visibility == dbVisibilityPrivate {
		shared := dbVisibilityShared
		if err := h.inTxAsOnConn(r.Context(), conn, user.PgID(), func(q Querier) error {
			_, txErr := applyMetadataPatch(r.Context(), q, pgID, metadataPatch{Visibility: &shared})
			return txErr
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update transcript visibility")
			return
		}
	}

	shares, _ := q.ListTranscriptShares(r.Context(), pgID)
	writeJSON(w, http.StatusOK, shares)
}

func (h *Handler) UnshareTranscript(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}
	groupID, err := uuid.Parse(chi.URLParam(r, "groupID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}

	pgID := toPgUUID(id)
	transcript, err := h.queries.GetTranscriptByID(r.Context(), pgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Transcript not found")
		return
	}
	if transcript.OwnerID != user.PgID() {
		writeError(w, http.StatusForbidden, "Not the transcript owner")
		return
	}

	h.queries.UnshareTranscript(r.Context(), sqlc.UnshareTranscriptParams{
		TranscriptID: pgID,
		GroupID:      toPgUUID(groupID),
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "unshared"})
}

func (h *Handler) ListTranscripts(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	q := r.URL.Query()

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	baseFrom := `FROM transcripts t
		LEFT JOIN transcript_tags tt ON t.id = tt.transcript_id
		LEFT JOIN tags tg ON tt.tag_id = tg.id
		LEFT JOIN transcript_shares ts ON t.id = ts.transcript_id
		LEFT JOIN group_members gm ON ts.group_id = gm.group_id`

	conditions := []string{}
	args := []any{}
	argIdx := 1

	if user != nil {
		conditions = append(conditions, fmt.Sprintf(
			`(t.visibility = 'public' OR t.owner_id = $%d OR (t.visibility = 'shared' AND gm.user_id = $%d))`,
			argIdx, argIdx,
		))
		args = append(args, user.PgID())
		argIdx++
	} else {
		conditions = append(conditions, `t.visibility = 'public'`)
	}

	if search := q.Get("q"); search != "" {
		conditions = append(conditions, fmt.Sprintf(
			`(t.title ILIKE $%d OR t.description ILIKE $%d)`, argIdx, argIdx,
		))
		args = append(args, "%"+search+"%")
		argIdx++
	}

	if provider := q.Get("provider"); provider != "" {
		conditions = append(conditions, fmt.Sprintf(`t.model_provider = $%d`, argIdx))
		args = append(args, provider)
		argIdx++
	}

	if owner := q.Get("owner"); owner != "" {
		conditions = append(conditions, fmt.Sprintf(
			`t.owner_id = (SELECT id FROM users WHERE github_username = $%d)`, argIdx,
		))
		args = append(args, owner)
		argIdx++
	}

	if project := q.Get("project"); project != "" {
		conditions = append(conditions, fmt.Sprintf(`t.project_name ILIKE $%d`, argIdx))
		args = append(args, "%"+project+"%")
		argIdx++
	}

	if repo := q.Get("repo"); repo != "" {
		conditions = append(conditions, fmt.Sprintf(`t.git_remote ILIKE $%d`, argIdx))
		args = append(args, "%"+repo+"%")
		argIdx++
	}

	if org := q.Get("org"); org != "" {
		conditions = append(conditions, fmt.Sprintf(
			`t.owner_id IN (SELECT user_id FROM user_github_orgs WHERE lower(org_login) = lower($%d) AND visible = true)`,
			argIdx,
		))
		args = append(args, org)
		argIdx++
	}

	if tagsParam := q.Get("tags"); tagsParam != "" {
		tagNames := strings.Split(tagsParam, ",")
		placeholders := make([]string, len(tagNames))
		for i, name := range tagNames {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, strings.TrimSpace(name))
			argIdx++
		}
		conditions = append(conditions, fmt.Sprintf(`tg.name IN (%s)`, strings.Join(placeholders, ",")))
	}

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	orderBy := " ORDER BY t.published_at DESC"
	switch q.Get("sort") {
	case "turns":
		orderBy = " ORDER BY t.turn_count DESC NULLS LAST"
	case "tokens":
		orderBy = " ORDER BY t.token_count DESC NULLS LAST"
	case "duration":
		orderBy = " ORDER BY t.duration_ms DESC NULLS LAST"
	}

	var total int64
	countQuery := "SELECT count(DISTINCT t.id) " + baseFrom + where
	err := h.pool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to count transcripts")
		return
	}

	// Name-addressed scanning (db tags + RowToStructByName): the SELECT and the
	// struct can't drift apart, so a migration's new column can never again
	// silently serialize as null on this surface (the license_id regression), and
	// a scan error is a loud 500 instead of a silently dropped row.
	selectQuery := "SELECT DISTINCT " + transcriptSelectColumns + " " +
		baseFrom + where + orderBy + fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)

	rows, err := h.pool.Query(r.Context(), selectQuery, append(args, limit, offset)...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list transcripts")
		return
	}
	listed, err := pgx.CollectRows(rows, pgx.RowToStructByName[sqlc.Transcript])
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list transcripts")
		return
	}

	type parsedTranscript struct {
		t     sqlc.Transcript
		tags  []sqlc.Tag
		owner sqlc.User
	}
	var parsed []parsedTranscript
	for _, t := range listed {
		tags, _ := h.queries.GetTranscriptTags(r.Context(), t.ID)
		owner, _ := h.queries.GetUserByID(r.Context(), t.OwnerID)
		parsed = append(parsed, parsedTranscript{t: t, tags: tags, owner: owner})
	}

	// Batch-fetch signalling data for all transcripts
	var transcriptIDs []pgtype.UUID
	ownerIDSet := map[pgtype.UUID]bool{}
	var ownerIDs []pgtype.UUID
	for _, p := range parsed {
		transcriptIDs = append(transcriptIDs, p.t.ID)
		if !ownerIDSet[p.t.OwnerID] {
			ownerIDSet[p.t.OwnerID] = true
			ownerIDs = append(ownerIDs, p.t.OwnerID)
		}
	}

	// Batch org badges by owner
	orgsByOwner := map[pgtype.UUID][]sqlc.ListVisibleOrgsByUserIDsRow{}
	if len(ownerIDs) > 0 {
		allOrgs, _ := h.queries.ListVisibleOrgsByUserIDs(r.Context(), ownerIDs)
		for _, o := range allOrgs {
			orgsByOwner[o.UserID] = append(orgsByOwner[o.UserID], o)
		}
	}

	// Batch shares by transcript
	sharesByTranscript := map[pgtype.UUID][]sqlc.ListSharesByTranscriptIDsRow{}
	if len(transcriptIDs) > 0 {
		allShares, _ := h.queries.ListSharesByTranscriptIDs(r.Context(), transcriptIDs)
		for _, s := range allShares {
			sharesByTranscript[s.TranscriptID] = append(sharesByTranscript[s.TranscriptID], s)
		}
	}

	// Batch attestations by transcript
	attestsByTranscript := map[pgtype.UUID][]sqlc.ListAttestationsByTranscriptIDsRow{}
	if len(transcriptIDs) > 0 {
		allAttests, _ := h.queries.ListAttestationsByTranscriptIDs(r.Context(), transcriptIDs)
		for _, a := range allAttests {
			attestsByTranscript[a.TranscriptID] = append(attestsByTranscript[a.TranscriptID], a)
		}
	}

	transcripts := []map[string]any{}
	for _, p := range parsed {
		transcripts = append(transcripts, map[string]any{
			"transcript":   listTranscriptResponse(p.t),
			"tags":         p.tags,
			"owner":        p.owner,
			"owner_orgs":   orgsByOwner[p.t.OwnerID],
			"shares":       sharesByTranscript[p.t.ID],
			"attestations": attestsByTranscript[p.t.ID],
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"transcripts": transcripts,
		"total":       total,
		"page":        page,
		"limit":       limit,
	})
}

func (h *Handler) canViewTranscript(ctx context.Context, user *AuthUser, t sqlc.Transcript) bool {
	if t.Visibility == "public" {
		return true
	}
	if user == nil {
		return false
	}
	if t.OwnerID == user.PgID() {
		return true
	}
	if t.Visibility == "shared" {
		shares, err := h.queries.ListTranscriptShares(ctx, t.ID)
		if err != nil {
			return false
		}
		for _, share := range shares {
			_, err := h.queries.GetGroupMember(ctx, sqlc.GetGroupMemberParams{
				GroupID: share.GroupID,
				UserID:  user.PgID(),
			})
			if err == nil {
				return true
			}
		}
	}
	// Collective owners can preview transcripts shared with their collective
	// (including private/pending submissions awaiting approval).
	owners, err := h.queries.ListGroupOwnersForTranscript(ctx, t.ID)
	if err == nil {
		callerPg := user.PgID()
		for _, ownerID := range owners {
			if ownerID == callerPg {
				return true
			}
		}
	}
	return false
}

// persistCommits replaces a transcript's stored git commits with the payload's
// set using the supplied Querier (which may be transaction-bound). It is
// idempotent w.r.t. re-publish: all existing commit rows for the transcript are
// DELETEd first, then the payload's commits are written in ONE batched multi-row
// upsert, replacing the prior per-row InsertTranscriptCommit
// loop's N round-trips with a single jsonb_to_recordset INSERT...ON CONFLICT.
//
// Taking a Querier (rather than reaching through h.queries) lets the caller pass
// a tx-bound *sqlc.Queries so the DELETE + INSERT are atomic (see
// inTxAs + persistCommits), while unit tests pass a mock to assert the post-state.
func persistCommits(ctx context.Context, q Querier, transcriptID pgtype.UUID, commits []schema.CommitInfo) error {
	if err := q.DeleteTranscriptCommits(ctx, transcriptID); err != nil {
		return err
	}
	// Dedup by SHA before building the single-statement payload: a multi-row
	// INSERT ... ON CONFLICT cannot affect the same (transcript_id, sha) twice in
	// one statement, so a payload with a repeated SHA would error and roll back
	// the whole commit set. Real git SHAs within one transcript are unique, but
	// deduping (last-wins, matching the upsert's DO UPDATE) preserves the prior
	// per-row loop's tolerance for a malformed payload.
	records := dedupeCommitsBySHA(schemaToCommitRecords(commits))
	if len(records) == 0 {
		// Nothing to insert; the DELETE already cleared any stale set.
		return nil
	}
	payload, err := json.Marshal(records)
	if err != nil {
		return err
	}
	return q.InsertTranscriptCommits(ctx, sqlc.InsertTranscriptCommitsParams{
		TranscriptID: transcriptID,
		Commits:      payload,
	})
}

// dedupeCommitsBySHA returns the records with duplicate SHAs collapsed to a
// single entry, keeping each SHA's LAST occurrence (matching ON CONFLICT DO
// UPDATE last-wins semantics). The relative order of the kept records is stable.
func dedupeCommitsBySHA(records []commitJSONRecord) []commitJSONRecord {
	if len(records) < 2 {
		return records
	}
	lastIdx := make(map[string]int, len(records))
	for i, r := range records {
		lastIdx[r.Sha] = i
	}
	out := make([]commitJSONRecord, 0, len(records))
	for i, r := range records {
		if lastIdx[r.Sha] == i {
			out = append(out, r)
		}
	}
	return out
}

// optionalString maps an empty string to nil (JSON null / SQL NULL).
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// optionalMillis maps unset (negative) Unix-millis to nil; 0 ms is a valid time.
// Mirrors int64ToTimestamptz's null semantics for the JSON/recordset boundary.
func optionalMillis(ms int64) *time.Time {
	if ms < 0 {
		return nil
	}
	t := time.UnixMilli(ms)
	return &t
}

// commitJSONRecord is the per-commit shape consumed by the InsertTranscriptCommits
// query's jsonb_to_recordset. Pointer fields preserve SQL NULLs across the JSON
// boundary (a nil pointer marshals to JSON null → a NULL column), which parallel
// text[]/int[] arrays cannot express. Timestamps marshal as RFC3339, which
// Postgres parses back into timestamptz.
type commitJSONRecord struct {
	CommitOrder int32      `json:"commit_order"`
	Sha         string     `json:"sha"`
	Message     *string    `json:"message"`
	AuthorName  *string    `json:"author_name"`
	AuthorEmail *string    `json:"author_email"`
	Additions   *int32     `json:"additions"`
	Deletions   *int32     `json:"deletions"`
	AuthoredAt  *time.Time `json:"authored_at"`
	CommittedAt *time.Time `json:"committed_at"`
}

func (h *Handler) linkTags(ctx context.Context, transcriptID pgtype.UUID, tagNames []string) error {
	return linkTagsWithQuerier(ctx, h.queries, transcriptID, tagNames)
}

func linkTagsWithQuerier(ctx context.Context, q Querier, transcriptID pgtype.UUID, tagNames []string) error {
	if err := q.UnlinkTranscriptTags(ctx, transcriptID); err != nil {
		return err
	}
	for _, name := range tagNames {
		name = strings.TrimSpace(strings.ToLower(name))
		if name == "" {
			continue
		}
		tag, err := q.GetOrCreateTag(ctx, name)
		if err != nil {
			return err
		}
		if err := q.LinkTranscriptTag(ctx, sqlc.LinkTranscriptTagParams{
			TranscriptID: transcriptID,
			TagID:        tag.ID,
		}); err != nil {
			return err
		}
	}
	return nil
}
