-- Pull request prompt-attachment store (migration 037).
--
-- `state` is written by exactly one statement, UpdatePullRequestAttachmentState,
-- and only from internal/promptattach.Transition, which enforces the closed
-- transition table in Go. Every other statement here reads the state or writes
-- rows keyed to an already-decided attachment.

-- name: CreatePullRequestAttachment :one
-- Records (or re-observes) the attachment row for one pull request. A repeated
-- observation of the same (github_repo_id, number) refreshes the head and
-- remotes it was seen at, but never resets the lifecycle state.
INSERT INTO pull_request_attachments (
    repo_owner, repo_name, github_repo_id, number, head_sha, base_remote, head_remote,
    author_id, requester_github_id, state, requested_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now()
)
ON CONFLICT (github_repo_id, number) DO UPDATE SET
    repo_owner   = EXCLUDED.repo_owner,
    repo_name    = EXCLUDED.repo_name,
    head_sha     = EXCLUDED.head_sha,
    base_remote  = EXCLUDED.base_remote,
    head_remote  = EXCLUDED.head_remote,
    updated_at   = now()
RETURNING *;

-- name: GetPullRequestAttachment :one
SELECT * FROM pull_request_attachments WHERE id = $1;

-- name: GetPullRequestAttachmentForPull :one
-- Addresses an attachment the way the routes do: repository owner/name plus the
-- pull request number, case-insensitively on the owner and name.
SELECT * FROM pull_request_attachments
WHERE lower(repo_owner) = lower($1) AND lower(repo_name) = lower($2) AND number = $3;

-- name: UpdatePullRequestAttachmentState :one
-- The ONLY statement that changes pull_request_attachments.state. The
-- `state = expected_state` predicate makes the write conditional on the state
-- the caller read, so a concurrent transition loses cleanly (no row) instead of
-- overwriting another move. Stamps the target state's timestamp.
UPDATE pull_request_attachments
SET state        = sqlc.arg(state),
    updated_at   = now(),
    requested_at = CASE WHEN sqlc.arg(state) = 'requested' THEN now() ELSE requested_at END,
    waiting_at   = CASE WHEN sqlc.arg(state) = 'waiting'   THEN now() ELSE waiting_at   END,
    preview_at   = CASE WHEN sqlc.arg(state) = 'preview'   THEN now() ELSE preview_at   END,
    attached_at  = CASE WHEN sqlc.arg(state) = 'attached'  THEN now() ELSE attached_at  END,
    detached_at  = CASE WHEN sqlc.arg(state) = 'detached'  THEN now() ELSE detached_at  END
WHERE id = sqlc.arg(id) AND state = sqlc.arg(expected_state)
RETURNING *;

-- name: AttachPullRequestTranscript :exec
-- Binds a transcript to an attachment at a position, recording the visibility
-- the transcript held before an attach widened it so detach can restore exactly
-- that value. Idempotent on the (attachment, transcript) key.
INSERT INTO pull_request_attachment_transcripts (
    attachment_id, transcript_id, position, previous_visibility
) VALUES (
    $1, $2, $3, $4
)
ON CONFLICT (attachment_id, transcript_id) DO UPDATE SET
    position            = EXCLUDED.position,
    previous_visibility = EXCLUDED.previous_visibility;

-- name: ListPullRequestAttachmentTranscripts :many
SELECT * FROM pull_request_attachment_transcripts
WHERE attachment_id = $1
ORDER BY position ASC, transcript_id ASC;
