-- name: UnshareTranscript :exec
-- The owner withdraws their contribution.
--
-- Two shapes, because an accepted contribution and a submission awaiting review
-- are different things. A submission still awaiting review is closed in place:
-- nothing was decided, and leaving it open would block the owner from ever
-- offering that transcript again. An ACCEPTED contribution is history - it was
-- accepted, by someone, on a date - so withdrawing it appends a further
-- attempt rather than overwriting the acceptance.
--
-- Either way the latest attempt ends up 'retracted' and the derivation removes
-- the current-state row.
WITH live AS (
    SELECT event_num, status
    FROM transcript_share_attempts
    WHERE transcript_id = $1 AND group_id = $2
      AND status IN ('pending', 'approved')
    ORDER BY event_num DESC
    LIMIT 1
), closed_open_submission AS (
    UPDATE transcript_share_attempts t
    SET status = 'retracted', decided_at = now()
    FROM live
    WHERE t.transcript_id = $1 AND t.group_id = $2
      AND t.event_num = live.event_num
      AND live.status = 'pending'
    RETURNING t.event_num
)
INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status, decided_at)
SELECT $1, $2, live.event_num + 1, 'retracted', now()
FROM live
WHERE live.status = 'approved';

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
       t.encryption_algorithm, t.key_version, t.session_origin,
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
-- The collective removes a contribution. 'revoked' is kept distinct from
-- 'retracted' because the actor differs: the collective removed it rather than
-- the owner withdrawing it, and conflating the two makes the history unreadable
-- for the person whose transcript it is.
--
-- Same two shapes as a withdrawal: a submission awaiting review is closed in
-- place, an accepted contribution gets a further attempt so its acceptance
-- stays on record. A submission awaiting review is closed too - before the
-- attempt model this route removed the share whatever its state, and leaving it
-- open would keep it in the review queue and block re-submission forever.
WITH live AS (
    SELECT event_num, status
    FROM transcript_share_attempts
    WHERE group_id = $1 AND transcript_id = $2
      AND status IN ('pending', 'approved')
    ORDER BY event_num DESC
    LIMIT 1
), closed_open_submission AS (
    UPDATE transcript_share_attempts t
    SET status = 'revoked', decided_at = now()
    FROM live
    WHERE t.group_id = $1 AND t.transcript_id = $2
      AND t.event_num = live.event_num
      AND live.status = 'pending'
    RETURNING t.event_num
)
INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status, decided_at)
SELECT $2, $1, live.event_num + 1, 'revoked', now()
FROM live
WHERE live.status = 'approved';

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
-- A moderator decides the open attempt. Only a still-open attempt can be
-- decided; a decided attempt is history, and changing a decision means a new
-- submission and a new attempt.
UPDATE transcript_share_attempts
SET status = $3, decided_at = now(), decided_by = $4
WHERE transcript_id = $1
  AND group_id = $2
  AND status = 'pending';

-- name: ShareTranscriptWithStatus :exec
-- The owner submits the transcript to a collective, opening the next attempt.
-- A rejected, retracted or revoked history does not block a new submission -
-- that is the point of counting attempts - so there is deliberately no
-- ON CONFLICT DO NOTHING here: a duplicate submission while one is already
-- live is refused by uq_share_attempt_open rather than silently discarded.
INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
SELECT $1, $2, COALESCE(MAX(event_num), 0) + 1, $3
FROM transcript_share_attempts
WHERE transcript_id = $1 AND group_id = $2;

-- name: GetLatestShareAttempt :one
-- The most recent attempt for a (transcript, collective) pair, or no row when
-- the transcript was never submitted there. This is what the share path reads
-- to tell a genuine re-submission from a duplicate one.
SELECT id, transcript_id, group_id, event_num, status,
       recorded_at, decided_at, decided_by
FROM transcript_share_attempts
WHERE transcript_id = $1 AND group_id = $2
ORDER BY event_num DESC
LIMIT 1;

-- name: ListShareAttempts :many
-- The full submission history for a (transcript, collective) pair, oldest
-- first. Every rejection and every withdrawal is its own row.
SELECT id, transcript_id, group_id, event_num, status,
       recorded_at, decided_at, decided_by
FROM transcript_share_attempts
WHERE transcript_id = $1 AND group_id = $2
ORDER BY event_num;

-- name: ListOwnerCollectiveSubmissions :many
-- EVERY (transcript, collective) pair the caller has ever offered to ONE
-- collective, with the latest recorded event of each pair.
--
-- The source is the ATTEMPT LEDGER, never the derived current-state row, and
-- that is the whole point of the statement. The derived row is a fold that
-- keeps only live states: a pair whose last event was a retraction or a
-- revocation has no derived row at all. A listing built on the derived row
-- therefore reported nothing for a contribution that was submitted, refused and
-- then withdrawn - the person was told they had refusals and then shown an
-- empty list. Reading the ledger keeps the fully-withdrawn pair listed, so its
-- history stays reachable.
--
-- Owner-only: $2 is the authenticated caller, and there is deliberately no
-- username parameter and no username route, so no request can name a subject
-- other than the caller. A caller with no pair here gets an empty result, which
-- the handler answers as 404 - the same answer as a collective that does not
-- exist, so asking cannot be used to discover one.
-- $1 = group_id, $2 = owner_id.
SELECT latest.transcript_id, latest.group_id, latest.title, latest.event_num,
       latest.status, latest.recorded_at
FROM (
  SELECT DISTINCT ON (a.transcript_id)
         a.transcript_id, a.group_id, t.title, a.event_num, a.status, a.recorded_at
  FROM transcript_share_attempts a
  JOIN transcripts t ON t.id = a.transcript_id
  WHERE a.group_id = $1 AND t.owner_id = $2
  ORDER BY a.transcript_id, a.event_num DESC
) latest
ORDER BY latest.recorded_at DESC, latest.transcript_id;

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
-- Closes every live attempt in the given collective whose transcript is owned
-- by the given user. Used when a member leaves and chooses to retract, or when
-- the collective's deletion policy is 'mandatory', so nothing of theirs is left
-- open in a collective they have left.
--
-- Per transcript this is the same two shapes as a single withdrawal: a
-- submission awaiting review is closed in place, an accepted contribution gets
-- a further attempt so its acceptance stays on record.
WITH live AS (
    SELECT DISTINCT ON (a.transcript_id)
           a.transcript_id, a.event_num, a.status
    FROM transcript_share_attempts a
    JOIN transcripts t ON t.id = a.transcript_id
    WHERE a.group_id = $1
      AND t.owner_id = $2
      AND a.status IN ('pending', 'approved')
    ORDER BY a.transcript_id, a.event_num DESC
), closed_open_submissions AS (
    UPDATE transcript_share_attempts a
    SET status = 'retracted', decided_at = now()
    FROM live
    WHERE a.group_id = $1
      AND a.transcript_id = live.transcript_id
      AND a.event_num = live.event_num
      AND live.status = 'pending'
    RETURNING a.event_num
)
INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status, decided_at)
SELECT live.transcript_id, $1, live.event_num + 1, 'retracted', now()
FROM live
WHERE live.status = 'approved';

-- name: ListGroupOwnersForTranscript :many
-- Returns the owner user_ids of every collective that has a share row
-- (any status) for the given transcript. Used to grant collective owners
-- read access on pending submissions before they approve.
SELECT DISTINCT gm.user_id
FROM transcript_shares ts
JOIN group_members gm ON gm.group_id = ts.group_id
WHERE ts.transcript_id = $1 AND gm.role = 'owner';

-- name: ListLiveShareAttemptsForGroup :many
-- Which of the named transcripts are ALREADY live in one collective. Live means
-- the latest event of the (transcript, collective) pair is pending or approved,
-- which is exactly what makes a further submission a duplicate rather than the
-- next attempt.
--
-- This statement reads the attempt LEDGER (transcript_share_attempts) and
-- nothing else. It deliberately never mentions the derived transcript_shares
-- row: a pair that was submitted, refused and then withdrawn has no derived row,
-- and a batch that consulted the projection would report an open submission as
-- absent and then be refused by the database instead of answering the person.
SELECT latest.transcript_id, latest.status
FROM (
    SELECT DISTINCT ON (a.transcript_id)
           a.transcript_id, a.status
    FROM transcript_share_attempts a
    WHERE a.group_id = @group_id
      AND a.transcript_id = ANY(@transcript_ids::uuid[])
    ORDER BY a.transcript_id, a.event_num DESC
) latest
WHERE latest.status IN ('pending', 'approved');
