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
-- root is a transcript with no parent_session_id, whose parent is not among
-- the owner's transcripts, or that is on a parent loop (walking up its parents
-- comes back to it). The leading loop_walk declaration is never read: sqlc
-- cannot resolve a WITH nested in a subquery, and the inner WITH shadows it in
-- PostgreSQL. live is true only when the latest attempt of the
-- (transcript, collective) pair in the append-only ledger is pending or
-- approved, never read from the derived projection.
WITH RECURSIVE loop_walk(local_id, parent_session_id, seen) AS (
    SELECT NULL::text, NULL::text, NULL::text[] WHERE false
)
SELECT t.id, t.local_id, t.project_hash, t.visibility,
       (t.parent_session_id IS NULL OR NOT EXISTS (
        SELECT 1
        FROM transcripts p
        WHERE p.owner_id = t.owner_id
          AND p.local_id = t.parent_session_id)
       OR (
        EXISTS (
            SELECT 1
            FROM transcripts loop_child
            WHERE loop_child.owner_id = t.owner_id
              AND loop_child.parent_session_id = t.local_id
        )
        AND EXISTS (
            WITH RECURSIVE loop_walk(local_id, parent_session_id, seen) AS (
                SELECT t.local_id, t.parent_session_id, ARRAY[t.local_id]
                UNION ALL
                SELECT loop_step.local_id, loop_step.parent_session_id, loop_walk.seen || loop_step.local_id
                FROM loop_walk
                JOIN transcripts loop_step
                  ON loop_step.owner_id = t.owner_id
                 AND loop_step.local_id = loop_walk.parent_session_id
                WHERE loop_step.local_id <> ALL (loop_walk.seen)
            )
            SELECT 1
            FROM loop_walk
            WHERE loop_walk.parent_session_id = t.local_id
        )
       ))::boolean AS is_root,
       COALESCE(a.status, '')::text AS latest_status,
       COALESCE(a.status IN ('pending', 'approved'), false)::boolean AS live
FROM transcripts t
LEFT JOIN LATERAL (SELECT s.status FROM transcript_share_attempts s WHERE s.transcript_id = t.id AND s.group_id = @group_id ORDER BY s.event_num DESC LIMIT 1) a ON true
WHERE t.owner_id = @owner_id
  AND t.id = ANY(@transcript_ids::uuid[])
ORDER BY t.id;
