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

-- name: ListOwnerProjectIdentities :many
-- One row per (owner_id, project_hash) pair carrying every piece of evidence the
-- display-name resolver needs, aggregated in ONE statement so a page of transcripts
-- spanning many owners and many projects never issues a query per row.
--
-- The candidate project names are returned as an ORDERED ARRAY rather than as a
-- pre-classified "consented name" and "privacy label" pair on purpose: telling those
-- two apart is the job of projectname.IsPrivacyLabel, and expressing that rule a
-- second time as a SQL regex would create two definitions of the same thing that
-- must stay identical forever. The ordering (published_at DESC, then id) is the
-- deterministic pick the resolver's contract requires, so the caller only has to
-- walk the array and take the first name of each class.
--
-- project_path is picked by the SAME deterministic idiom as git_remote — the
-- first non-empty value under (published_at DESC, then id) — so the two pieces of
-- evidence a project falls back to cannot disagree about which transcript they
-- came from. It is the path the publishing client recorded, ALREADY redacted when
-- it reached storage; this read neither re-derives nor masks it.
--
-- The override join is narrowed to the single writable pair, ('project',
-- 'display_name'); the table reserves other pairs for later fields, and this read
-- must not start returning one the application does not implement.
SELECT
    t.owner_id,
    t.project_hash,
    COALESCE(o.value, '')::text AS override_name,
    COALESCE(
        array_agg(t.project_name ORDER BY t.published_at DESC, t.id)
            FILTER (WHERE t.project_name IS NOT NULL AND t.project_name <> ''),
        ARRAY[]::text[]
    )::text[] AS project_names,
    COALESCE(
        (array_agg(t.git_remote ORDER BY t.published_at DESC, t.id)
            FILTER (WHERE t.git_remote IS NOT NULL AND t.git_remote <> ''))[1],
        ''
    )::text AS git_remote,
    COALESCE(
        (array_agg(t.project_path ORDER BY t.published_at DESC, t.id)
            FILTER (WHERE t.project_path IS NOT NULL AND t.project_path <> ''))[1],
        ''
    )::text AS project_path,
    count(*)::bigint AS transcript_count
FROM transcripts t
LEFT JOIN owner_overrides o
       ON o.owner_id = t.owner_id
      AND o.target_kind = 'project'
      AND o.field = 'display_name'
      AND o.target_key = t.project_hash
WHERE t.owner_id = ANY(@owner_ids::uuid[])
  AND t.project_hash = ANY(@project_hashes::text[])
GROUP BY t.owner_id, t.project_hash, o.value
ORDER BY t.owner_id, t.project_hash;

-- name: CountOwnerTranscriptsInProject :one
-- Ownership probe for the project-correction endpoints: an owner may only name a
-- project they have actually published into. It counts rather than returning a row
-- because the caller needs the yes/no answer, not the transcripts.
SELECT count(*)::bigint
FROM transcripts
WHERE owner_id = @owner_id AND project_hash = @project_hash;

-- name: ListProjectTranscriptsForViewer :many
-- The transcripts of one owner's project that one viewer may see. The predicate is
-- the SHIPPED web read policy, character-for-character the same disjunction the
-- discovery listing applies (public, or the viewer's own, or shared into a
-- collective the viewer belongs to); this route deliberately introduces no new
-- visibility rule of its own. An anonymous viewer passes a NULL id, which makes the
-- owner and membership arms unsatisfiable and leaves only the public arm.
--
-- The membership arm is an EXISTS rather than a join so a transcript shared into
-- several of the viewer's collectives is still returned exactly once, with no
-- DISTINCT over the whole wide row.
SELECT t.id, t.owner_id, t.local_id, t.title, t.description, t.visibility, t.model_provider,
    t.model_name, t.harness_version, t.session_start, t.session_end, t.turn_count, t.token_count,
    t.blob_key, t.blob_size_bytes, t.schema_version, t.published_at, t.updated_at, t.parent_session_id,
    t.ingested_at, t.source_file_path, t.source_format, t.git_branch, t.git_remote, t.git_worktree,
    t.project_hash, t.project_path, t.project_name, t.tool_call_count, t.subagent_count, t.duration_ms,
    t.subagents, t.diagnostics_warnings, t.diagnostics_partial, t.tokens_in, t.tokens_out,
    t.title_generated, t.outcome, t.files_touched, t.lines_changed, t.retry_loops, t.retry_tokens_wasted,
    t.within_session_reverts, t.signal_density, t.spec_quality_score, t.exploration_ratio,
    t.scope_breadth, t.discovery_turns, t.m2_token_outcome_ratio, t.m3_unique_tool_count,
    t.m4_error_recovery_count, t.m4_consecutive_error_max, t.m5_context_utilization_pct,
    t.m5_peak_context_tokens, t.m5_avg_message_tokens, t.m6_output_survival_pct,
    t.m6_lines_survived, t.m6_lines_total, t.m7_spec_word_count, t.m7_spec_has_examples,
    t.m7_spec_has_constraints, t.computed_at, t.compute_version, t.content_hash, t.license_id,
    t.wrapped_data_key, t.encryption_algorithm, t.key_version,
    t.accepted_request_operation_fingerprint, t.session_origin
FROM transcripts t
WHERE t.owner_id = @owner_id
  AND t.project_hash = @project_hash
  AND (
        t.visibility = 'public'
        OR t.owner_id = @viewer_id
        OR (t.visibility = 'shared' AND EXISTS (
                SELECT 1
                FROM transcript_shares ts
                JOIN group_members gm ON gm.group_id = ts.group_id
                WHERE ts.transcript_id = t.id AND gm.user_id = @viewer_id))
      )
ORDER BY t.published_at DESC, t.id DESC;

-- Content-hash and pull-list queries live in the sibling source file
-- queries/transcripts_pull.sql, mirroring the
-- annotations_push convention so sqlc round-trips them into transcripts_pull.sql.go
-- without colliding with this file's generated transcripts.sql.go.

-- name: ListOwnerProjectShareCandidates :many
-- Every transcript of ONE owner in ONE project: the closed set a batch
-- contribution to a collective may name. The batch route resolves the request's
-- ids against this set BEFORE it opens a transaction, so a transcript belonging
-- to another project or another person is refused with nothing written.
--
-- Ownership and project identity are both in the WHERE clause rather than being
-- checked afterwards in Go, so a row that reaches the caller is by construction
-- one the caller may contribute. The ordering is the deterministic pick the rest
-- of the identity surfaces already use (published_at DESC, then id), so the
-- receipt lists the transcripts in the same order the person sees them.
SELECT t.id, t.local_id, t.visibility
FROM transcripts t
WHERE t.owner_id = @owner_id
  AND t.project_hash = @project_hash
ORDER BY t.published_at DESC, t.id ASC;

-- name: ListOwnerContributableTranscripts :many
-- Every transcript the caller owns, each carrying whether it is ALREADY live in
-- ONE collective. The contribute surface needs both halves at once: the corpus
-- it groups into projects and branches, and the per-row answer that decides
-- which rows it may still offer.
--
-- already_shared is computed here, from the attempt LEDGER, and never from the
-- derived current-state row: a pair whose last event was a withdrawal has no
-- derived row at all, and a caller that read the projection would present a
-- withdrawn contribution as contributable while it is not, or the reverse.
-- Live means the LATEST event of the pair is pending or approved, which is the
-- same definition the single share path applies before it opens an attempt.
SELECT t.id, t.local_id, t.title, t.visibility, t.project_hash, t.git_branch,
       t.parent_session_id, t.session_origin, t.model_provider, t.published_at,
       EXISTS (
           SELECT 1
           FROM transcript_share_attempts a
           WHERE a.transcript_id = t.id
             AND a.group_id = @group_id
             AND a.status IN ('pending', 'approved')
             AND a.event_num = (
                 SELECT max(b.event_num)
                 FROM transcript_share_attempts b
                 WHERE b.transcript_id = t.id AND b.group_id = @group_id
             )
       )::boolean AS already_shared
FROM transcripts t
WHERE t.owner_id = @owner_id
ORDER BY t.published_at DESC, t.id ASC;
