-- name: ListGroupedTranscriptCandidates :many
-- No pagination here: group counts and top-level pages share this exact selected
-- set. EXISTS avoids duplicating identities when tags or share memberships join.
SELECT t.* FROM transcripts t
WHERE (
    t.visibility = 'public'
    OR t.owner_id = sqlc.narg(viewer_id)::uuid
    OR (t.visibility = 'shared' AND EXISTS (
        SELECT 1 FROM transcript_shares ts
        JOIN group_members gm ON gm.group_id = ts.group_id
        WHERE ts.transcript_id = t.id AND gm.user_id = sqlc.narg(viewer_id)::uuid
    ))
)
AND (sqlc.arg(search_text)::text = '' OR t.title ILIKE sqlc.arg(search_pattern)::text OR t.description ILIKE sqlc.arg(search_pattern)::text)
AND (sqlc.arg(provider)::text = '' OR t.model_provider = sqlc.arg(provider)::text)
AND (sqlc.arg(owner_name)::text = '' OR t.owner_id = (SELECT id FROM users WHERE github_username = sqlc.arg(owner_name)::text))
AND (sqlc.arg(project_name)::text = '' OR t.project_name ILIKE sqlc.arg(project_pattern)::text)
AND (sqlc.arg(project_hash)::text = '' OR t.project_hash = sqlc.arg(project_hash)::text)
AND (sqlc.arg(repository)::text = '' OR t.git_remote ILIKE sqlc.arg(repository_pattern)::text)
AND (sqlc.arg(org)::text = '' OR t.owner_id IN (
    SELECT user_id FROM user_github_orgs WHERE lower(org_login) = lower(sqlc.arg(org)::text) AND visible = true
))
AND (cardinality(sqlc.arg(tags)::text[]) = 0 OR EXISTS (
    SELECT 1 FROM transcript_tags tt JOIN tags tg ON tg.id = tt.tag_id
    WHERE tt.transcript_id = t.id AND tg.name = ANY(sqlc.arg(tags)::text[])
))
AND (
    (sqlc.arg(origin)::text <> '' AND t.session_origin = sqlc.arg(origin)::text)
    OR (sqlc.arg(origin)::text = '' AND (t.session_origin <> 'agent' OR t.session_purpose = 'helper_review'))
)
ORDER BY coalesce(t.session_start, t.published_at) DESC, t.id ASC;

-- name: ListGroupedCyclicHelpers :many
-- A filtered-out ancestor must not change a helper's group identity. Read only
-- owner-local edge identities to classify cycles; never return ancestor rows,
-- titles, content, timestamps, or counts to the caller.
WITH RECURSIVE parents AS (
    SELECT t.owner_id, t.local_id,
        CASE WHEN relation.value IS NOT NULL THEN
            CASE WHEN relation.value->>'targetState' IN ('target_known', 'target_known_retained')
                 THEN relation.value->>'targetLocalId' END
        ELSE t.parent_session_id END AS parent_local_id
    FROM transcripts t
    LEFT JOIN LATERAL (
        SELECT edge.value FROM jsonb_array_elements(t.session_relationships) AS edge(value)
        WHERE edge.value->>'kind' = 'started_by'
    ) relation ON true
    WHERE t.owner_id IN (SELECT owner_id FROM transcripts WHERE id=ANY(sqlc.arg(transcript_ids)::uuid[]))
), walk AS (
    SELECT t.id AS transcript_id, p.owner_id, p.parent_local_id,
           ARRAY[p.local_id]::text[] AS visited, false AS cyclic
    FROM transcripts t JOIN parents p ON p.owner_id=t.owner_id AND p.local_id=t.local_id
    WHERE t.id = ANY(sqlc.arg(transcript_ids)::uuid[]) AND t.session_purpose='helper_review'
    UNION ALL
    SELECT w.transcript_id, p.owner_id, p.parent_local_id,
           w.visited || p.local_id, p.local_id=ANY(w.visited)
    FROM walk w JOIN parents p ON p.owner_id=w.owner_id AND p.local_id=w.parent_local_id
    WHERE NOT w.cyclic
)
SELECT DISTINCT transcript_id FROM walk WHERE cyclic;
