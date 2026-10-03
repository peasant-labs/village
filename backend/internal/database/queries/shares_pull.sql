-- Pull-share queries live in this dedicated source file so sqlc emits them into
-- shares_pull.sql.go without colliding with shares.sql.go. This mirrors the
-- annotations_push.sql source and generated-file layout.

-- name: ListApprovedTranscriptShareGroups :many
-- Returns the group IDs for a transcript's approved shares.
-- Direct reads and pulls check member grants only against APPROVED shares.
-- Pending/rejected submissions grant no member access; the web read separately
-- permits collective owners to preview submissions awaiting their review.
SELECT ts.group_id
FROM transcript_shares ts
WHERE ts.transcript_id = $1 AND ts.status = 'approved';
