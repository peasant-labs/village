-- name: ListContributionPage :many
-- One page of contribution families for one owner and one collective: each
-- root session of the page together with every helper that descends from it.
-- The page is bounded in ROOT sessions (root_limit), never in transcripts, so
-- a root's helpers ride with it and are not counted.
--
-- A root is a transcript of the owner with no parent_session_id, or whose
-- parent is not among the owner's published transcripts. A parent_session_id is
-- a LOCAL session id, unique only within one owner, so every parent lookup and
-- every descent step is scoped to owner_id.
--
-- A family belongs to its root's project (project_hash narrows by the ROOT's
-- project). A helper in another project still appears in its root's family and
-- carries its own project_hash.
--
-- Order. Roots are ordered by session time, newest first, with the transcript
-- id as the tie-break: COALESCE(session_start, published_at, epoch). The cursor
-- is that pair, so a page resumes strictly after the last root it returned and
-- a row inserted meanwhile can neither repeat a root nor skip one.
--
-- Filters narrow FAMILIES, not rows. q and provider are combined with AND, and a
-- family qualifies when its root OR any helper satisfies each one. The rule is
-- defined once, in handler/contribution_family.go, and the fragments below are
-- its canonical text with named placeholders; a drift test holds them equal.
-- q matches a member whose title contains the search (title_pattern, an escaped
-- ILIKE pattern), whose transcript id equals it ignoring case, or whose local
-- session id equals it exactly. A ROOT also matches when its own project is one
-- of matching_projects (the caller resolves project display names in Go, because
-- the name is a tiered resolution over several tables and is not a column); a
-- helper's project never matches. matched is reported per member, and only when
-- q was sent.
--
-- The unfiltered path stays a bounded read: the family-wide filter expansion is
-- seeded only when q or provider is present, so with neither the root query is
-- ordered and cut at root_limit before any family is expanded. There is no
-- index on parent_session_id, so the descent is written set-at-a-time (one
-- recursive join per level over the whole working set), never as one
-- correlated recursion per root.
--
-- Each transcript has exactly one parent, so the descent from a root is a tree
-- and terminates without a depth cap. A transcript whose parent chain forms a
-- cycle is never a descendant of a root and never appears.
--
-- latest_status is the status of the LATEST attempt of the (transcript,
-- collective) pair in the append-only ledger, never the derived projection;
-- it is the empty string when the pair has no attempt. matched is meaningful
-- only when q was sent.
-- already_shared is true only for pending and approved: the one live definition
-- every contribution read shares.
WITH RECURSIVE
all_roots AS NOT MATERIALIZED (
    SELECT t.id, t.project_hash,
           COALESCE(t.session_start, t.published_at, 'epoch'::timestamptz) AS sort_time
    FROM transcripts t
    WHERE t.owner_id = @owner_id
      AND (
          t.parent_session_id IS NULL
          OR NOT EXISTS (
              SELECT 1
              FROM transcripts p
              WHERE p.owner_id = t.owner_id
                AND p.local_id = t.parent_session_id
          )
      )
),
filter_family(root_id, id, local_id, depth) AS (
    SELECT r.id, t.id, t.local_id, 0
    FROM all_roots r
    JOIN transcripts t ON t.id = r.id
    WHERE (sqlc.narg('q')::text IS NOT NULL OR sqlc.narg('provider')::text IS NOT NULL)
    UNION ALL
    SELECT f.root_id, c.id, c.local_id, f.depth + 1
    FROM filter_family f
    JOIN transcripts c ON c.owner_id = @owner_id AND c.parent_session_id = f.local_id
),
filter_member(root_id, id, matched, has_provider) AS (
    SELECT f.root_id, f.id,
           CASE WHEN sqlc.narg('q')::text IS NULL THEN NULL ELSE (
               (COALESCE(t.title ILIKE sqlc.narg('title_pattern')::text, false) OR lower(t.id::text) = lower(sqlc.narg('q')::text) OR t.local_id = sqlc.narg('q')::text)
               OR (f.depth = 0 AND COALESCE(t.project_hash = ANY(@matching_projects::text[]), false))
           ) END,
           COALESCE(t.model_provider = sqlc.narg('provider')::text, false)
    FROM filter_family f
    JOIN transcripts t ON t.id = f.id
),
qualifying_roots AS (
    SELECT root_id
    FROM filter_member
    GROUP BY root_id
    HAVING (sqlc.narg('q')::text IS NULL OR bool_or(matched))
       AND (sqlc.narg('provider')::text IS NULL OR bool_or(has_provider))
),
page_roots AS (
    SELECT r.id, r.sort_time
    FROM all_roots r
    WHERE (sqlc.narg('project_hash')::text IS NULL OR r.project_hash = sqlc.narg('project_hash')::text)
      AND (
          (sqlc.narg('q')::text IS NULL AND sqlc.narg('provider')::text IS NULL)
          OR r.id IN (SELECT root_id FROM qualifying_roots)
      )
      AND (
          sqlc.narg('after_id')::uuid IS NULL
          OR r.sort_time < sqlc.narg('after_time')::timestamptz
          OR (r.sort_time = sqlc.narg('after_time')::timestamptz AND r.id > sqlc.narg('after_id')::uuid)
      )
    ORDER BY r.sort_time DESC, r.id ASC
    LIMIT @root_limit::int
),
family(root_id, id, local_id, depth) AS (
    SELECT r.id, t.id, t.local_id, 0
    FROM page_roots r
    JOIN transcripts t ON t.id = r.id
    UNION ALL
    SELECT f.root_id, c.id, c.local_id, f.depth + 1
    FROM family f
    JOIN transcripts c ON c.owner_id = @owner_id AND c.parent_session_id = f.local_id
)
SELECT f.root_id, f.depth,
       t.id, t.local_id, t.title, t.visibility, t.project_hash, t.git_branch,
       t.parent_session_id, t.session_origin, t.model_provider, t.published_at,
       t.session_start,
       COALESCE(ls.status, '')::text AS latest_status,
       COALESCE(ls.status IN ('pending', 'approved'), false)::boolean AS already_shared,
       COALESCE(ff.matched, false)::boolean AS matched
FROM family f
JOIN page_roots pr ON pr.id = f.root_id
JOIN transcripts t ON t.id = f.id
LEFT JOIN LATERAL (SELECT s.status FROM transcript_share_attempts s WHERE s.transcript_id = t.id AND s.group_id = @group_id ORDER BY s.event_num DESC LIMIT 1) ls ON true
LEFT JOIN filter_member ff ON ff.id = f.id
ORDER BY pr.sort_time DESC, pr.id ASC, f.depth ASC,
         COALESCE(t.session_start, t.published_at, 'epoch'::timestamptz) ASC, t.id ASC;
