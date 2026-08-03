-- name: UnshareTranscript :exec
DELETE FROM transcript_shares WHERE transcript_id = $1 AND group_id = $2;

-- name: ListTranscriptShares :many
SELECT ts.group_id, g.name as group_name, ts.shared_at
FROM transcript_shares ts
JOIN groups g ON ts.group_id = g.id
WHERE ts.transcript_id = $1;

-- The pull-share query ListApprovedTranscriptShareGroups lives in the sibling
-- source file queries/shares_pull.sql, mirroring the
-- annotations_push convention so sqlc round-trips it into shares_pull.sql.go
-- without colliding with this file's generated shares.sql.go.

-- name: ListGroupTranscripts :many
SELECT t.id, t.owner_id, t.local_id, t.title, t.description, t.visibility,
       t.model_provider, t.model_name, t.harness_version, t.session_start, t.session_end,
       t.turn_count, t.token_count, t.blob_key, t.blob_size_bytes, t.schema_version,
       t.published_at, t.updated_at, t.parent_session_id, t.ingested_at,
       t.source_file_path, t.source_format, t.git_branch, t.git_remote, t.git_worktree,
       t.project_hash, t.project_path, t.project_name, t.tool_call_count, t.subagent_count,
       t.duration_ms, t.subagents, t.diagnostics_warnings, t.diagnostics_partial,
       t.tokens_in, t.tokens_out, t.title_generated, t.outcome, t.files_touched,
       t.lines_changed, t.retry_loops, t.retry_tokens_wasted, t.within_session_reverts,
       t.signal_density, t.spec_quality_score, t.exploration_ratio, t.scope_breadth,
       t.discovery_turns, t.m2_token_outcome_ratio, t.m3_unique_tool_count,
       t.m4_error_recovery_count, t.m4_consecutive_error_max, t.m5_context_utilization_pct,
       t.m5_peak_context_tokens, t.m5_avg_message_tokens, t.m6_output_survival_pct,
       t.m6_lines_survived, t.m6_lines_total, t.m7_spec_word_count,
       t.m7_spec_has_examples, t.m7_spec_has_constraints, t.computed_at,
       t.compute_version, t.content_hash, t.license_id, t.wrapped_data_key,
       t.encryption_algorithm, t.key_version,
       u.github_username   AS owner_username,
       u.avatar_url        AS owner_avatar_url,
       u.is_discoverable   AS owner_is_discoverable
FROM transcripts t
JOIN transcript_shares ts ON t.id = ts.transcript_id
JOIN users u ON t.owner_id = u.id
WHERE ts.group_id = $1 AND ts.status = 'approved'
ORDER BY t.published_at DESC
LIMIT $2 OFFSET $3;

-- name: RemoveGroupTranscript :exec
DELETE FROM transcript_shares
WHERE group_id = $1 AND transcript_id = $2;

-- name: ListSharesByTranscriptIDs :many
SELECT ts.transcript_id, ts.group_id, g.name as group_name,
       g.acceptance_mode, ts.status, ts.shared_at
FROM transcript_shares ts
JOIN groups g ON ts.group_id = g.id
WHERE ts.transcript_id = ANY($1::uuid[]) AND ts.status = 'approved';

-- name: ListPendingGroupShares :many
SELECT ts.transcript_id, t.title, t.model_provider,
       u.github_username as owner_username,
       u.is_discoverable as owner_is_discoverable,
       ts.shared_at
FROM transcript_shares ts
JOIN transcripts t ON ts.transcript_id = t.id
JOIN users u ON t.owner_id = u.id
WHERE ts.group_id = $1 AND ts.status = 'pending'
ORDER BY ts.shared_at;

-- name: UpdateShareStatus :exec
UPDATE transcript_shares SET status = $3
WHERE transcript_id = $1 AND group_id = $2;

-- name: ShareTranscriptWithStatus :exec
INSERT INTO transcript_shares (transcript_id, group_id, status)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: IsTranscriptSharedWithGroup :one
SELECT EXISTS(
  SELECT 1 FROM transcript_shares
  WHERE transcript_id = $1 AND group_id = $2
) AS shared;

-- name: GetGroupTranscriptStats :one
SELECT
  COUNT(*)::int AS total_transcripts,
  COUNT(DISTINCT t.owner_id)::int AS contributor_count,
  COALESCE(SUM(t.turn_count), 0)::bigint AS total_turns,
  COALESCE(SUM(t.duration_ms), 0)::bigint AS total_duration_ms,
  COALESCE(SUM(COALESCE(t.tokens_in, 0) + COALESCE(t.tokens_out, 0)), 0)::bigint AS total_tokens
FROM transcripts t
JOIN transcript_shares ts ON t.id = ts.transcript_id
WHERE ts.group_id = $1 AND ts.status = 'approved';

-- name: ListGroupModelBreakdown :many
SELECT t.model_provider, COUNT(*)::int AS transcript_count
FROM transcripts t
JOIN transcript_shares ts ON t.id = ts.transcript_id
WHERE ts.group_id = $1 AND ts.status = 'approved'
GROUP BY t.model_provider
ORDER BY transcript_count DESC;

-- name: ListGroupContributors :many
SELECT u.id, u.github_username, u.avatar_url, COUNT(*)::int AS transcript_count
FROM transcripts t
JOIN transcript_shares ts ON t.id = ts.transcript_id
JOIN users u ON t.owner_id = u.id
WHERE ts.group_id = $1 AND ts.status = 'approved'
  AND (u.is_discoverable = TRUE OR @viewer_is_owner::boolean)
GROUP BY u.id, u.github_username, u.avatar_url
ORDER BY transcript_count DESC;

-- name: ListUserSharesInGroup :many
-- Returns the caller's own transcripts shared with a given collective
-- (both approved and pending). Used to power the "Your contributions" card
-- on the collective page.
SELECT t.id, t.title, t.model_provider, t.model_name, t.visibility,
       t.published_at, t.turn_count, t.tokens_in, t.tokens_out,
       ts.status, ts.shared_at
FROM transcript_shares ts
JOIN transcripts t ON ts.transcript_id = t.id
WHERE ts.group_id = $1 AND t.owner_id = $2
ORDER BY ts.shared_at DESC;

-- name: RetractUserSharesInGroup :exec
-- Removes every share row in the given collective where the underlying
-- transcript is owned by the given user. Used when a member leaves and
-- chooses to retract (or when the collective's policy is 'mandatory').
DELETE FROM transcript_shares
WHERE group_id = $1
  AND transcript_id IN (
    SELECT id FROM transcripts WHERE owner_id = $2
  );

-- name: ListGroupOwnersForTranscript :many
-- Returns the owner user_ids of every collective that has a share row
-- (any status) for the given transcript. Used to grant collective owners
-- read access on pending submissions before they approve.
SELECT DISTINCT gm.user_id
FROM transcript_shares ts
JOIN group_members gm ON gm.group_id = ts.group_id
WHERE ts.transcript_id = $1 AND gm.role = 'owner';
