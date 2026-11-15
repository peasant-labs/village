-- name: ListOwnerSelectionShareStates :many
-- The share state of exactly the listed transcripts of one owner towards one
-- collective: the selection write reads it once before it takes any lock and
-- again inside its locked transaction. The read is bounded by the listed ids
-- (the primary key), never by the owner's project, so a selection costs what it
-- names however large the owner's library is.
--
-- An id that is not the owner's transcript is simply absent from the result,
-- so the caller cannot tell another owner's transcript from one that does not
-- exist.
--
-- is_root and live are the family rule of handler/contribution_family.go, and
-- the fragments below are its canonical text; a drift test holds them equal. A
-- root is a transcript with no parent_session_id, or whose parent is not among
-- the owner's transcripts. live is true only when the latest attempt of the
-- (transcript, collective) pair in the append-only ledger is pending or
-- approved, never read from the derived projection.
SELECT t.id, t.local_id, t.project_hash, t.visibility,
       (t.parent_session_id IS NULL OR NOT EXISTS (SELECT 1 FROM transcripts p WHERE p.owner_id = t.owner_id AND p.local_id = t.parent_session_id))::boolean AS is_root,
       COALESCE(a.status, '')::text AS latest_status,
       COALESCE(a.status IN ('pending', 'approved'), false)::boolean AS live
FROM transcripts t
LEFT JOIN LATERAL (SELECT s.status FROM transcript_share_attempts s WHERE s.transcript_id = t.id AND s.group_id = @group_id ORDER BY s.event_num DESC LIMIT 1) a ON true
WHERE t.owner_id = @owner_id
  AND t.id = ANY(@transcript_ids::uuid[])
ORDER BY t.id;
