-- name: ListCollectiveGroupedCandidates :many
-- Complete candidates, never a flat transcript page. The grouped pager and
-- member replay consume this identical set. route_kind is a server-selected
-- closed variant, not a caller-supplied SQL or authorization expression.
SELECT sqlc.embed(t),
       u.github_username AS owner_username,
       u.avatar_url AS owner_avatar_url,
       u.is_discoverable AS owner_is_discoverable,
       COALESCE(ts.status, '')::text AS share_status,
       ts.shared_at,
       EXISTS (
           SELECT 1 FROM transcript_share_attempts a
           WHERE a.transcript_id = t.id AND a.group_id = @group_id
             AND a.status IN ('pending', 'approved')
             AND a.event_num = (
                 SELECT max(b.event_num) FROM transcript_share_attempts b
                 WHERE b.transcript_id = t.id AND b.group_id = @group_id
             )
       )::boolean AS already_shared
FROM transcripts t
JOIN users u ON u.id = t.owner_id
JOIN groups g ON g.id = @group_id
LEFT JOIN group_members gm ON gm.group_id = g.id AND gm.user_id = sqlc.narg(viewer_id)
LEFT JOIN transcript_shares ts ON ts.transcript_id = t.id AND ts.group_id = g.id
WHERE (
    (@route_kind::text = 'collective' AND ts.status = 'approved' AND (
        g.data_access = 'public'
        OR (g.data_access = 'contributors' AND gm.role IN ('contributor', 'member', 'owner'))
        OR (g.data_access = 'members_only' AND gm.role IN ('member', 'owner'))
    ))
    OR (@route_kind::text = 'pending' AND ts.status = 'pending' AND gm.role = 'owner')
    OR (@route_kind::text = 'my-shares' AND ts.transcript_id IS NOT NULL AND t.owner_id = sqlc.narg(viewer_id))
    OR (@route_kind::text = 'contributable' AND gm.user_id IS NOT NULL AND t.owner_id = sqlc.narg(viewer_id)
        AND NOT EXISTS (
            SELECT 1 FROM transcript_share_attempts a
            WHERE a.transcript_id = t.id AND a.group_id = g.id
              AND a.status IN ('pending', 'approved')
              AND a.event_num = (
                  SELECT max(b.event_num) FROM transcript_share_attempts b
                  WHERE b.transcript_id = t.id AND b.group_id = g.id
              )
        ))
)
AND (@project_hash::text = '' OR t.project_hash = @project_hash)
AND (@search::text = '' OR t.title ILIKE '%' || @search || '%' OR t.description ILIKE '%' || @search || '%')
ORDER BY COALESCE(t.session_start, t.published_at) DESC, t.id ASC;
