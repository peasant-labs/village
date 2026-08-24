-- name: CreateTranscript :one
INSERT INTO transcripts (
    id, owner_id, local_id, title, description, visibility,
    model_provider, model_name, harness_version,
    session_start, session_end, turn_count, token_count,
    blob_key, blob_size_bytes, schema_version,
    parent_session_id, ingested_at, source_file_path, source_format,
    git_branch, git_remote, git_worktree,
    project_hash, project_path, project_name,
    tool_call_count, subagent_count, duration_ms,
    subagents, diagnostics_warnings, diagnostics_partial,
    tokens_in, tokens_out, title_generated, outcome,
    files_touched, lines_changed, retry_loops, retry_tokens_wasted,
    within_session_reverts, signal_density, spec_quality_score, exploration_ratio,
    scope_breadth, discovery_turns, m2_token_outcome_ratio,
    m3_unique_tool_count, m4_error_recovery_count, m4_consecutive_error_max,
    m5_context_utilization_pct, m5_peak_context_tokens, m5_avg_message_tokens,
    m6_output_survival_pct, m6_lines_survived, m6_lines_total,
    m7_spec_word_count, m7_spec_has_examples, m7_spec_has_constraints,
    computed_at, compute_version, license_id,
    content_hash, wrapped_data_key, encryption_algorithm, key_version,
    session_origin
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
    $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31,
    $32, $33, $34, $35, $36, $37, $38, $39, $40, $41, $42, $43, $44, $45, $46,
    $47, $48, $49, $50, $51, $52, $53, $54, $55, $56, $57, $58, $59, $60, $61,
    $62, $63, $64, $65, $66, $67
) RETURNING id, owner_id, local_id, title, description, visibility, model_provider,
    model_name, harness_version, session_start, session_end, turn_count, token_count,
    blob_key, blob_size_bytes, schema_version, published_at, updated_at, parent_session_id,
    ingested_at, source_file_path, source_format, git_branch, git_remote, git_worktree,
    project_hash, project_path, project_name, tool_call_count, subagent_count, duration_ms,
    subagents, diagnostics_warnings, diagnostics_partial, tokens_in, tokens_out,
    title_generated, outcome, files_touched, lines_changed, retry_loops, retry_tokens_wasted,
    within_session_reverts, signal_density, spec_quality_score, exploration_ratio,
    scope_breadth, discovery_turns, m2_token_outcome_ratio, m3_unique_tool_count,
    m4_error_recovery_count, m4_consecutive_error_max, m5_context_utilization_pct,
    m5_peak_context_tokens, m5_avg_message_tokens, m6_output_survival_pct,
    m6_lines_survived, m6_lines_total, m7_spec_word_count, m7_spec_has_examples,
    m7_spec_has_constraints, computed_at, compute_version, content_hash, license_id,
    wrapped_data_key, encryption_algorithm, key_version, accepted_request_operation_fingerprint, session_origin;

-- name: UpdateTranscriptByOwnerAndLocalID :one
UPDATE transcripts SET
    title = $3,
    description = $4,
    visibility = $5,
    model_provider = $6,
    model_name = $7,
    harness_version = $8,
    session_start = $9,
    session_end = $10,
    turn_count = $11,
    token_count = $12,
    blob_key = $13,
    blob_size_bytes = $14,
    schema_version = $15,
    parent_session_id = $16,
    ingested_at = $17,
    source_file_path = $18,
    source_format = $19,
    git_branch = $20,
    git_remote = $21,
    git_worktree = $22,
    project_hash = $23,
    project_path = $24,
    project_name = $25,
    tool_call_count = $26,
    subagent_count = $27,
    duration_ms = $28,
    subagents = $29,
    diagnostics_warnings = $30,
    diagnostics_partial = $31,
    tokens_in = $32,
    tokens_out = $33,
    title_generated = $34,
    outcome = $35,
    files_touched = $36,
    lines_changed = $37,
    retry_loops = $38,
    retry_tokens_wasted = $39,
    within_session_reverts = $40,
    signal_density = $41,
    spec_quality_score = $42,
    exploration_ratio = $43,
    scope_breadth = $44,
    discovery_turns = $45,
    m2_token_outcome_ratio = $46,
    m3_unique_tool_count = $47,
    m4_error_recovery_count = $48,
    m4_consecutive_error_max = $49,
    m5_context_utilization_pct = $50,
    m5_peak_context_tokens = $51,
    m5_avg_message_tokens = $52,
    m6_output_survival_pct = $53,
    m6_lines_survived = $54,
    m6_lines_total = $55,
    m7_spec_word_count = $56,
    m7_spec_has_examples = $57,
    m7_spec_has_constraints = $58,
    computed_at = $59,
    compute_version = $60,
    license_id = $61,
    content_hash = $62,
    wrapped_data_key = $63,
    encryption_algorithm = $64,
    key_version = $65,
    session_origin = $66,
    updated_at = now()
WHERE owner_id = $1 AND local_id = $2
RETURNING id, owner_id, local_id, title, description, visibility, model_provider,
    model_name, harness_version, session_start, session_end, turn_count, token_count,
    blob_key, blob_size_bytes, schema_version, published_at, updated_at, parent_session_id,
    ingested_at, source_file_path, source_format, git_branch, git_remote, git_worktree,
    project_hash, project_path, project_name, tool_call_count, subagent_count, duration_ms,
    subagents, diagnostics_warnings, diagnostics_partial, tokens_in, tokens_out,
    title_generated, outcome, files_touched, lines_changed, retry_loops, retry_tokens_wasted,
    within_session_reverts, signal_density, spec_quality_score, exploration_ratio,
    scope_breadth, discovery_turns, m2_token_outcome_ratio, m3_unique_tool_count,
    m4_error_recovery_count, m4_consecutive_error_max, m5_context_utilization_pct,
    m5_peak_context_tokens, m5_avg_message_tokens, m6_output_survival_pct,
    m6_lines_survived, m6_lines_total, m7_spec_word_count, m7_spec_has_examples,
    m7_spec_has_constraints, computed_at, compute_version, content_hash, license_id,
    wrapped_data_key, encryption_algorithm, key_version, accepted_request_operation_fingerprint, session_origin;

-- name: GetTranscriptIDByOwnerAndLocalID :one
-- Publish-path existence probe (create vs re-publish). ID-only ON PURPOSE: the
-- governance pre-image comes from the LOCKED narrow read inside the txn
-- (GetTranscriptGovernanceForUpdate); a wide unlocked read here would be dead
-- weight superseded under the lock.
SELECT id FROM transcripts WHERE owner_id = $1 AND local_id = $2;

-- name: GetTranscriptByID :one
SELECT id, owner_id, local_id, title, description, visibility, model_provider,
    model_name, harness_version, session_start, session_end, turn_count, token_count,
    blob_key, blob_size_bytes, schema_version, published_at, updated_at, parent_session_id,
    ingested_at, source_file_path, source_format, git_branch, git_remote, git_worktree,
    project_hash, project_path, project_name, tool_call_count, subagent_count, duration_ms,
    subagents, diagnostics_warnings, diagnostics_partial, tokens_in, tokens_out,
    title_generated, outcome, files_touched, lines_changed, retry_loops, retry_tokens_wasted,
    within_session_reverts, signal_density, spec_quality_score, exploration_ratio,
    scope_breadth, discovery_turns, m2_token_outcome_ratio, m3_unique_tool_count,
    m4_error_recovery_count, m4_consecutive_error_max, m5_context_utilization_pct,
    m5_peak_context_tokens, m5_avg_message_tokens, m6_output_survival_pct,
    m6_lines_survived, m6_lines_total, m7_spec_word_count, m7_spec_has_examples,
    m7_spec_has_constraints, computed_at, compute_version, content_hash, license_id,
    wrapped_data_key, encryption_algorithm, key_version, accepted_request_operation_fingerprint, session_origin
FROM transcripts WHERE id = $1;

-- name: SetAcceptedRequestOperationFingerprint :exec
UPDATE transcripts
SET accepted_request_operation_fingerprint = $2
WHERE id = $1;

-- name: GetTranscriptGovernanceForUpdate :one
-- Locks the row (FOR UPDATE) so a mutation can resolve a partial patch / pin
-- republish state against the pre-image under one lock, serialising concurrent
-- edits to the same transcript. NARROW on purpose: the row lock is column-list-
-- independent, and only these five fields are ever resolved — the old SELECT *
-- hauled ~60 columns (incl. JSONB) per edit for nothing. The audit events
-- themselves are written by the migration-026 triggers, not by callers of this.
SELECT id, title, description, visibility, license_id
FROM transcripts WHERE id = $1 FOR UPDATE;

-- name: UpdateTranscriptMetadata :one
UPDATE transcripts SET
    title = $2,
    description = $3,
    visibility = $4,
    license_id = $5,
    updated_at = now()
WHERE id = $1
RETURNING id, owner_id, local_id, title, description, visibility, model_provider,
    model_name, harness_version, session_start, session_end, turn_count, token_count,
    blob_key, blob_size_bytes, schema_version, published_at, updated_at, parent_session_id,
    ingested_at, source_file_path, source_format, git_branch, git_remote, git_worktree,
    project_hash, project_path, project_name, tool_call_count, subagent_count, duration_ms,
    subagents, diagnostics_warnings, diagnostics_partial, tokens_in, tokens_out,
    title_generated, outcome, files_touched, lines_changed, retry_loops, retry_tokens_wasted,
    within_session_reverts, signal_density, spec_quality_score, exploration_ratio,
    scope_breadth, discovery_turns, m2_token_outcome_ratio, m3_unique_tool_count,
    m4_error_recovery_count, m4_consecutive_error_max, m5_context_utilization_pct,
    m5_peak_context_tokens, m5_avg_message_tokens, m6_output_survival_pct,
    m6_lines_survived, m6_lines_total, m7_spec_word_count, m7_spec_has_examples,
    m7_spec_has_constraints, computed_at, compute_version, content_hash, license_id,
    wrapped_data_key, encryption_algorithm, key_version, accepted_request_operation_fingerprint, session_origin;

-- name: DeleteTranscript :exec
DELETE FROM transcripts WHERE id = $1;

-- name: RenameUserProject :execrows
UPDATE transcripts
SET project_name = $3, updated_at = now()
WHERE owner_id = $1 AND project_name = $2;

-- Content-hash and pull-list queries live in the sibling source file
-- queries/transcripts_pull.sql, mirroring the
-- annotations_push convention so sqlc round-trips them into transcripts_pull.sql.go
-- without colliding with this file's generated transcripts.sql.go.
