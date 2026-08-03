-- Pull-share queries live in this dedicated source file so sqlc emits them into
-- shares_pull.sql.go without colliding with shares.sql.go. This mirrors the
-- annotations_push.sql source and generated-file layout.

-- name: ListApprovedTranscriptShareGroups :many
-- Returns the group IDs for a transcript's approved shares.
-- canPullTranscript is stricter than canViewTranscript here: a
-- pending/rejected share does NOT grant pull access, so the policy fn checks the
-- requester's membership only against APPROVED shares (the divergence-table row
-- "group-shared, acceptance pending/rejected => deny").
SELECT ts.group_id
FROM transcript_shares ts
WHERE ts.transcript_id = $1 AND ts.status = 'approved';
