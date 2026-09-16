-- Pull request prompt-attachment store (migration 037).
--
-- `state` is written by exactly one statement, UpdatePullRequestAttachmentState,
-- and only from internal/promptattach.Transition, which enforces the closed
-- transition table in Go. Every other statement here reads the state or writes
-- rows keyed to an already-decided attachment.

-- name: CreatePullRequestAttachment :one
-- Records (or re-observes) the attachment row for one pull request, bound to the
-- collective whose link enabled it. Creation initialises the closed lifecycle at
-- 'requested' with its timestamp; every later move goes through the Go
-- transition function. A repeated observation of the same (github_repo_id,
-- number) refreshes the head and remotes it was seen at, but never resets the
-- lifecycle state and never re-binds the collective: consent belongs to the
-- collective that first enabled the attachment.
INSERT INTO pull_request_attachments (
    group_id, repo_owner, repo_name, github_repo_id, number, head_sha, base_remote, head_remote,
    author_id, requester_github_id, state, requested_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'requested', now()
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
WHERE lower(repo_owner) = lower($1) AND lower(repo_name) = lower($2) AND number = $3
ORDER BY created_at DESC, id DESC
LIMIT 1;

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
-- Binds a transcript to an attachment at a position, recording the visibility the
-- transcript held before an attach widened it so detach can restore exactly that
-- value. Idempotent on the (attachment, transcript) key: re-binding updates the
-- position but PRESERVES the original previous_visibility, because the snapshot
-- describes the transcript before the FIRST widening. A refresh or retry after the
-- transcript was widened must not overwrite it with the already-widened tier.
INSERT INTO pull_request_attachment_transcripts (
    attachment_id, transcript_id, position, previous_visibility
) VALUES (
    $1, $2, $3, $4
)
ON CONFLICT (attachment_id, transcript_id) DO UPDATE SET
    position = EXCLUDED.position;

-- name: ListPullRequestAttachmentTranscripts :many
SELECT * FROM pull_request_attachment_transcripts
WHERE attachment_id = $1
ORDER BY position ASC, transcript_id ASC;

-- name: SetPullRequestAttachmentDigest :exec
-- Stores the digest a preview computed without changing the state: a preview
-- exposes the digest on the pull request page but shares and posts nothing.
UPDATE pull_request_attachments
SET digest = @digest, updated_at = now()
WHERE id = @id;

-- name: SetPullRequestAttachmentArtifacts :exec
-- Records what an attach posted and against which commit. head_sha moves when a
-- new head is attached or refreshed; comment_id and check_run_id are the GitHub
-- objects a later refresh edits and a detach deletes or resets.
UPDATE pull_request_attachments
SET head_sha     = @head_sha,
    comment_id   = @comment_id,
    check_run_id = @check_run_id,
    digest       = @digest,
    updated_at   = now()
WHERE id = @id;

-- name: ListAuthorWaitingPromptRequests :many
-- The prompt requests waiting on one author, oldest first, for
-- GET /users/me/prompt-requests. A waiting attachment is one a non-author asked
-- for and no matching publish has completed yet.
SELECT * FROM pull_request_attachments
WHERE author_id = @author_id AND state = 'waiting'
ORDER BY requested_at ASC NULLS LAST, updated_at ASC, id ASC;

-- name: ListPullRequestAttachmentTranscriptSummaries :many
-- The transcripts one attachment holds, with the fields the response shows, in
-- attachment order. Reads the binding and the transcript together so the caller
-- does not stitch two queries per row.
SELECT pt.transcript_id, pt.position, pt.previous_visibility, t.title, t.session_start
FROM pull_request_attachment_transcripts pt
JOIN transcripts t ON t.id = pt.transcript_id
WHERE pt.attachment_id = @attachment_id
ORDER BY pt.position ASC, pt.transcript_id ASC;

-- name: DeletePullRequestAttachmentTranscripts :exec
-- Clears an attachment's transcript bindings. Called after a detach has restored
-- each transcript from its recorded previous_visibility, so the next attach
-- records the visibility that is true at that time rather than replaying a
-- snapshot from a cycle that is over.
DELETE FROM pull_request_attachment_transcripts WHERE attachment_id = $1;
