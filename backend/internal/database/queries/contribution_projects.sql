-- name: ListOwnerContributionRootProjects :many
-- The project of every root session the caller owns. A root is a session with
-- no parent_session_id, or whose parent is not among the caller's published
-- transcripts. A family belongs to its root's project, so these are the only
-- projects the contribution picker lists. Read once per request so project
-- names can be matched in Go before the counts are taken.
SELECT DISTINCT t.project_hash
FROM transcripts t
WHERE t.owner_id = @owner_id
  AND (t.parent_session_id IS NULL OR NOT EXISTS (
        SELECT 1
        FROM transcripts p
        WHERE p.owner_id = t.owner_id
          AND p.local_id = t.parent_session_id))
ORDER BY t.project_hash;

-- name: CountOwnerContributionFamilyMatches :many
-- Sessions in the matching families of the caller's roots, one row per root
-- project. A family is a root and every session that descends from it through
-- parent_session_id. It matches when the search is absent, when its root's
-- project was named by the caller's Go-side name match (@name_matched_projects),
-- or when any member's title matches the pattern or any member's id or local id
-- equals the exact text. A provider filter keeps only families holding a session
-- of that provider. Counts are sessions, never roots. selectable_count leaves out
-- sessions whose latest contribution event to the collective is pending or
-- approved, which is the same live definition the contribute listing uses.
WITH RECURSIVE roots AS (
  SELECT t.local_id, t.project_hash
  FROM transcripts t
  WHERE t.owner_id = @owner_id
    AND (t.parent_session_id IS NULL OR NOT EXISTS (
          SELECT 1
          FROM transcripts p
          WHERE p.owner_id = t.owner_id
            AND p.local_id = t.parent_session_id))
), family AS (
  SELECT r.project_hash AS root_project, r.local_id AS root_local, r.local_id AS local_id
  FROM roots r
  UNION
  SELECT f.root_project, f.root_local, h.local_id
  FROM family f
  JOIN transcripts h ON h.owner_id = @owner_id AND h.parent_session_id = f.local_id
), member AS (
  SELECT f.root_project,
         f.root_local,
         m.model_provider,
         (m.title ILIKE @title_pattern::text
           OR lower(m.id::text) = lower(@exact_text::text)
           OR m.local_id = @exact_text::text) AS text_match,
         EXISTS (
             SELECT 1
             FROM transcript_share_attempts a
             WHERE a.transcript_id = m.id
               AND a.group_id = @group_id
               AND a.status IN ('pending', 'approved')
               AND a.event_num = (
                   SELECT max(b.event_num)
                   FROM transcript_share_attempts b
                   WHERE b.transcript_id = m.id AND b.group_id = @group_id
               )
         ) AS live
  FROM family f
  JOIN transcripts m ON m.owner_id = @owner_id AND m.local_id = f.local_id
), fam AS (
  SELECT root_project,
         root_local,
         count(*) AS sessions,
         count(*) FILTER (WHERE NOT live) AS selectable,
         bool_or(text_match) AS text_hit,
         bool_or(model_provider = @provider::text) AS has_provider
  FROM member
  GROUP BY root_project, root_local
)
SELECT f.root_project AS project_hash,
       sum(f.sessions)::bigint AS matching_count,
       sum(f.selectable)::bigint AS selectable_count
FROM fam f
WHERE (NOT @has_query::boolean OR f.text_hit OR f.root_project = ANY(@name_matched_projects::text[]))
  AND (@provider::text = '' OR f.has_provider)
GROUP BY f.root_project;

-- name: CountOwnerContributionProviderMatches :many
-- The model provider of every session in the matching families, search applied
-- and the provider filter NOT applied, so the selector can list every provider
-- the search reaches. Counts are sessions. The family rule is the one in
-- CountOwnerContributionFamilyMatches.
WITH RECURSIVE roots AS (
  SELECT t.local_id, t.project_hash
  FROM transcripts t
  WHERE t.owner_id = @owner_id
    AND (t.parent_session_id IS NULL OR NOT EXISTS (
          SELECT 1
          FROM transcripts p
          WHERE p.owner_id = t.owner_id
            AND p.local_id = t.parent_session_id))
), family AS (
  SELECT r.project_hash AS root_project, r.local_id AS root_local, r.local_id AS local_id
  FROM roots r
  UNION
  SELECT f.root_project, f.root_local, h.local_id
  FROM family f
  JOIN transcripts h ON h.owner_id = @owner_id AND h.parent_session_id = f.local_id
), member AS (
  SELECT f.root_project,
         f.root_local,
         m.model_provider,
         (m.title ILIKE @title_pattern::text
           OR lower(m.id::text) = lower(@exact_text::text)
           OR m.local_id = @exact_text::text) AS text_match
  FROM family f
  JOIN transcripts m ON m.owner_id = @owner_id AND m.local_id = f.local_id
), fam AS (
  SELECT root_project,
         root_local,
         bool_or(text_match) AS text_hit
  FROM member
  GROUP BY root_project, root_local
)
SELECT m.model_provider AS provider,
       count(*)::bigint AS session_count
FROM member m
JOIN fam f ON f.root_project = m.root_project AND f.root_local = m.root_local
WHERE (NOT @has_query::boolean OR f.text_hit OR f.root_project = ANY(@name_matched_projects::text[]))
GROUP BY m.model_provider;
