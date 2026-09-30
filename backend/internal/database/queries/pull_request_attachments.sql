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

-- name: GetPullRequestAttachmentTranscript :one
-- One binding, for a compensation that must undo exactly the transcripts one
-- attempt widened rather than every binding the attachment holds.
SELECT * FROM pull_request_attachment_transcripts
WHERE attachment_id = $1 AND transcript_id = $2;

-- name: DeletePullRequestAttachmentTranscript :exec
-- Removes one binding, paired with restoring its recorded visibility.
DELETE FROM pull_request_attachment_transcripts
WHERE attachment_id = $1 AND transcript_id = $2;

-- name: ListAuthorAttachmentsForRepo :many
-- The author's attachments for one repository name in the states a publish can
-- move: a waiting request it can complete, or an attached one it can refresh.
-- Matched on the repository NAME, and never the owner: reponame.NormalizeRemote
-- reduces a remote to its last path segment, and a fork's clone keeps that name
-- while its owner changes, so the name is what makes a fork push match its
-- attachment at all. The matcher applies the same equality.
--
-- idx_pull_request_attachments_author_repo_name serves this predicate; the
-- (repo_owner, repo_name) index cannot, because it leads with a column this WHERE
-- never constrains, and the state is filtered rather than indexed.
SELECT * FROM pull_request_attachments
WHERE author_id = @author_id
  AND lower(repo_name) = lower(@repo_name)
  AND state = ANY(@states::text[])
ORDER BY updated_at ASC, id ASC;

-- name: SetPullRequestAttachmentRequester :one
-- Records which GitHub account asked the author to attach, so the author's
-- request list can say who is waiting on them.
UPDATE pull_request_attachments
SET requester_github_id = @requester_github_id, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: ListAttachmentsBindingTranscript :many
-- The attachments that bind one transcript. The owner's visibility change reads
-- them so an attachment stops advertising prompts that are no longer as visible
-- as the repository it belongs to requires. Only attached attachments can be
-- advertising anything.
SELECT a.* FROM pull_request_attachments a
JOIN pull_request_attachment_transcripts pt ON pt.attachment_id = a.id
WHERE pt.transcript_id = @transcript_id AND a.state = 'attached'
ORDER BY a.id ASC;

-- name: ListPullRequestCandidatesByTranscripts :many
-- The pull requests bound to a page of transcripts, in the requested states.
--
-- This and the two statements after it are the one candidate read behind every
-- pull request a reader is shown: the transcript's pull request list, the
-- summary on a list row, the collective's count, and the caller's own count.
-- Each returns one row per (transcript, attachment) binding with the repository
-- link the attachment's collective holds now and whether the viewer is a member
-- of that collective. The visibility rule itself is applied in Go
-- (pullRequestReadable), in the one place all four reads share, rather than
-- copied into each statement where the copies could drift apart.
--
-- The repository comes from the collective's CURRENT link, exactly as the
-- attachment read resolves it, so an attachment whose collective is gone or
-- whose repository is no longer linked has no row: its visibility check cannot
-- complete, and it is omitted rather than guessed. A pending join request is not
-- membership. Each is one statement for the whole page or the whole scope, never
-- one per row.
--
-- Only what the rule and the served rows need is selected - the pull request,
-- its state and author, and the repository link - never the attachment row
-- whole: its stored digest holds prompt text none of these reads serves, and a
-- collective's count would otherwise move every digest it binds to produce one
-- integer.
--
-- idx_pull_request_attachment_transcripts_transcript serves the transcript
-- predicate. Rows come newest first within a transcript: by the latest of
-- attached_at and detached_at, which for an attached attachment is when it was
-- attached.
SELECT pt.transcript_id,
       a.repo_owner, a.repo_name, a.number, a.state, a.author_id,
       cr.installation_id,
       cr.is_private,
       (gm.user_id IS NOT NULL)::boolean AS viewer_is_member
FROM pull_request_attachment_transcripts pt
JOIN pull_request_attachments a ON a.id = pt.attachment_id
JOIN collective_repositories cr
  ON cr.group_id = a.group_id
 AND lower(cr.owner) = lower(a.repo_owner)
 AND lower(cr.name) = lower(a.repo_name)
LEFT JOIN group_members gm
  ON gm.group_id = a.group_id
 AND gm.user_id = sqlc.narg(viewer_id)::uuid
 AND gm.role <> 'pending'
WHERE pt.transcript_id = ANY(@transcript_ids::uuid[])
  AND a.state = ANY(@states::text[])
ORDER BY pt.transcript_id,
         GREATEST(a.attached_at, a.detached_at) DESC NULLS LAST,
         lower(a.repo_owner), lower(a.repo_name), a.number, a.id;

-- name: ListAttachedPullRequestCandidatesByOwner :many
-- The attached pull requests bound to any transcript one person published, for
-- their own totals.
SELECT pt.transcript_id,
       a.repo_owner, a.repo_name, a.number, a.state, a.author_id,
       cr.installation_id,
       cr.is_private,
       (gm.user_id IS NOT NULL)::boolean AS viewer_is_member
FROM transcripts t
JOIN pull_request_attachment_transcripts pt ON pt.transcript_id = t.id
JOIN pull_request_attachments a ON a.id = pt.attachment_id
JOIN collective_repositories cr
  ON cr.group_id = a.group_id
 AND lower(cr.owner) = lower(a.repo_owner)
 AND lower(cr.name) = lower(a.repo_name)
LEFT JOIN group_members gm
  ON gm.group_id = a.group_id
 AND gm.user_id = sqlc.narg(viewer_id)::uuid
 AND gm.role <> 'pending'
WHERE t.owner_id = @owner_id
  AND a.state = 'attached'
ORDER BY a.id, pt.transcript_id;

-- name: ListAttachedPullRequestCandidatesByGroup :many
-- The attached pull requests bound to a transcript a collective counts: the
-- transcripts with an approved share to it, which is the set
-- GetGroupTranscriptStats totals.
SELECT pt.transcript_id,
       a.repo_owner, a.repo_name, a.number, a.state, a.author_id,
       cr.installation_id,
       cr.is_private,
       (gm.user_id IS NOT NULL)::boolean AS viewer_is_member
FROM transcript_shares ts
JOIN pull_request_attachment_transcripts pt ON pt.transcript_id = ts.transcript_id
JOIN pull_request_attachments a ON a.id = pt.attachment_id
JOIN collective_repositories cr
  ON cr.group_id = a.group_id
 AND lower(cr.owner) = lower(a.repo_owner)
 AND lower(cr.name) = lower(a.repo_name)
LEFT JOIN group_members gm
  ON gm.group_id = a.group_id
 AND gm.user_id = sqlc.narg(viewer_id)::uuid
 AND gm.role <> 'pending'
WHERE ts.group_id = @group_id
  AND ts.status = 'approved'
  AND a.state = 'attached'
ORDER BY a.id, pt.transcript_id;
