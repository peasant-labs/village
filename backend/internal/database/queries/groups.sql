-- name: CreateGroup :one
INSERT INTO groups (name, description, created_by, acceptance_mode, data_access, linked_github_org)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetGroupByID :one
SELECT * FROM groups WHERE id = $1;

-- name: UpdateGroup :one
UPDATE groups SET
    name = $2,
    description = $3,
    data_access = $4,
    acceptance_mode = $5,
    linked_github_org = $6,
    display_members = $7,
    transcript_deletion_policy = $8,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteGroup :exec
DELETE FROM groups WHERE id = $1;

-- name: ListUserGroups :many
-- member_count here deliberately EXCLUDES role='pending' members, to match
-- the members roster (ListGroupMembers, which also filters role != 'pending').
-- The 3 sibling list queries (ListAllGroups / SearchCollectives /
-- ListCollectivesByGitHubOrg) INCLUDE pending members in their member_count.
-- This per-surface split is intentional: the user-groups endpoint mirrors its
-- member roster, while discovery surfaces report all memberships.
SELECT g.id, g.name, g.description, g.created_by, g.created_at, g.updated_at,
       g.acceptance_mode, g.data_access, g.linked_github_org, g.display_members,
       g.transcript_deletion_policy,
       gm.role, gm.joined_at AS member_since,
       (SELECT COUNT(*) FROM group_members gm2
          WHERE gm2.group_id = g.id AND gm2.role != 'pending')::int AS member_count,
       (SELECT COUNT(*) FROM transcript_shares ts
          WHERE ts.group_id = g.id AND ts.status = 'approved')::int AS transcript_count
FROM groups g
JOIN group_members gm ON g.id = gm.group_id
WHERE gm.user_id = $1
ORDER BY g.name;

-- name: ListVisibleGroups :many
-- Every collective the caller may SEE, under the same visibility rule as
-- SearchCollectives: data_access = 'public', OR acceptance_mode = 'open', OR
-- the caller is a member. ListUserGroups above answers a different question -
-- which collectives the caller BELONGS to - and both are needed: a person
-- browsing collectives wants the full visible set, while a person choosing
-- where to contribute wants only their own memberships.
--
-- The join carries the caller's own membership only, so `role` and
-- `member_since` are NULL for a collective the caller sees through the public
-- or open rule alone. member_count excludes role='pending' to match
-- ListUserGroups and the members roster; transcript_count counts approved
-- shares, as it does there.
-- $1 = caller user_id.
SELECT g.id, g.name, g.description, g.created_by, g.created_at, g.updated_at,
       g.acceptance_mode, g.data_access, g.linked_github_org, g.display_members,
       g.transcript_deletion_policy,
       gm.role, gm.joined_at AS member_since,
       (SELECT COUNT(*) FROM group_members gm2
          WHERE gm2.group_id = g.id AND gm2.role != 'pending')::int AS member_count,
       (SELECT COUNT(*) FROM transcript_shares ts
          WHERE ts.group_id = g.id AND ts.status = 'approved')::int AS transcript_count
FROM groups g
LEFT JOIN group_members gm ON gm.group_id = g.id AND gm.user_id = $1
WHERE g.data_access = 'public'
   OR g.acceptance_mode = 'open'
   OR gm.user_id IS NOT NULL
ORDER BY g.name;

-- name: AddGroupMember :exec
INSERT INTO group_members (group_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (group_id, user_id) DO UPDATE SET role = EXCLUDED.role;

-- name: RemoveGroupMember :exec
DELETE FROM group_members WHERE group_id = $1 AND user_id = $2;

-- name: GetGroupMember :one
SELECT * FROM group_members WHERE group_id = $1 AND user_id = $2;

-- name: ListGroupMembers :many
SELECT gm.role, gm.joined_at, u.id, u.github_username, u.display_name, u.avatar_url,
       COALESCE(ARRAY_AGG(ugo.org_login) FILTER (WHERE ugo.org_login IS NOT NULL), '{}')::TEXT[] AS github_orgs
FROM group_members gm
JOIN users u ON gm.user_id = u.id
LEFT JOIN user_github_orgs ugo ON ugo.user_id = u.id AND ugo.visible = TRUE
WHERE gm.group_id = $1
  AND gm.role != 'pending'
  AND (u.is_discoverable = TRUE OR @viewer_is_owner::boolean)
GROUP BY gm.role, gm.joined_at, u.id, u.github_username, u.display_name, u.avatar_url
ORDER BY gm.role, u.github_username;

-- name: ListGroupPendingMembers :many
SELECT gm.role, gm.joined_at, u.id, u.github_username, u.display_name, u.avatar_url,
       COALESCE(ARRAY_AGG(ugo.org_login) FILTER (WHERE ugo.org_login IS NOT NULL), '{}')::TEXT[] AS github_orgs
FROM group_members gm
JOIN users u ON gm.user_id = u.id
LEFT JOIN user_github_orgs ugo ON ugo.user_id = u.id AND ugo.visible = TRUE
WHERE gm.group_id = $1
  AND gm.role = 'pending'
GROUP BY gm.role, gm.joined_at, u.id, u.github_username, u.display_name, u.avatar_url
ORDER BY gm.joined_at, u.github_username;

-- name: ListAllGroups :many
SELECT g.id, g.name, g.description, g.acceptance_mode, g.data_access, g.linked_github_org, g.created_at,
       COUNT(DISTINCT gm.user_id)::int AS member_count,
       COUNT(DISTINCT CASE WHEN ts.status = 'approved' THEN ts.transcript_id END)::int AS transcript_count
FROM groups g
LEFT JOIN group_members gm ON g.id = gm.group_id
LEFT JOIN transcript_shares ts ON g.id = ts.group_id
GROUP BY g.id
ORDER BY member_count DESC, g.name;

-- name: UpdateMemberRole :exec
UPDATE group_members SET role = $3 WHERE group_id = $1 AND user_id = $2;

-- name: SearchCollectives :many
-- Returns collectives matching the query, filtered to those the caller can see.
-- A collective is visible if:
--   - data_access = 'public', OR
--   - acceptance_mode = 'open', OR
--   - the caller (when authenticated) is a member.
-- $1 = query string (case-insensitive substring match against name/description/linked_github_org).
-- $2 = caller user_id (nullable: use NULL for unauthenticated callers).
-- $3 = limit.
SELECT g.id, g.name, g.description, g.linked_github_org,
       COUNT(DISTINCT gm.user_id)::int AS member_count,
       COUNT(DISTINCT CASE WHEN ts.status = 'approved' THEN ts.transcript_id END)::int AS transcript_count
FROM groups g
LEFT JOIN group_members gm ON g.id = gm.group_id
LEFT JOIN transcript_shares ts ON g.id = ts.group_id
WHERE (
        g.name ILIKE '%' || $1 || '%'
        OR g.description ILIKE '%' || $1 || '%'
        OR g.linked_github_org ILIKE '%' || $1 || '%'
      )
  AND (
        g.data_access = 'public'
        OR g.acceptance_mode = 'open'
        OR EXISTS (
            SELECT 1 FROM group_members gm2
            WHERE gm2.group_id = g.id AND gm2.user_id = $2
        )
      )
GROUP BY g.id
ORDER BY member_count DESC, transcript_count DESC
LIMIT $3;

-- name: ListCollectivesByGitHubOrg :many
-- Returns collectives linked to the given GitHub org (case-insensitive),
-- filtered to those the caller can see (same rules as SearchCollectives).
-- $1 = org login (case-insensitive match).
-- $2 = caller user_id (nullable: use NULL for unauthenticated callers).
SELECT g.id, g.name, g.description, g.linked_github_org,
       COUNT(DISTINCT gm.user_id)::int AS member_count,
       COUNT(DISTINCT CASE WHEN ts.status = 'approved' THEN ts.transcript_id END)::int AS transcript_count
FROM groups g
LEFT JOIN group_members gm ON g.id = gm.group_id
LEFT JOIN transcript_shares ts ON g.id = ts.group_id
WHERE lower(g.linked_github_org) = lower($1)
  AND (
        g.data_access = 'public'
        OR g.acceptance_mode = 'open'
        OR EXISTS (
            SELECT 1 FROM group_members gm2
            WHERE gm2.group_id = g.id AND gm2.user_id = $2
        )
      )
GROUP BY g.id
ORDER BY member_count DESC, transcript_count DESC;

-- name: ListOwnerCollectiveContributions :many
-- The collectives the caller has offered transcripts to, with the four
-- counters their profile renders, from ONE aggregate.
--
-- approved_count and pending_count count DISTINCT TRANSCRIPTS: a transcript is
-- either in a collective or it is not, so a contribution that was withdrawn and
-- accepted again is still one contribution. rejected_attempt_count and
-- withdrawn_attempt_count count EVENTS, because one transcript can be refused
-- or withdrawn by one collective repeatedly and each occurrence is its own
-- instance. The wire fields say attempt for that reason.
--
-- withdrawn_attempt_count counts retracted (the owner withdrew) and revoked
-- (the collective removed) events TOGETHER. Before it existed those events were
-- counted in none of the counters, so a contribution that was submitted,
-- refused and then withdrawn reported refusals with nothing to open: the
-- withdrawal itself was invisible on every surface. The two statuses stay
-- distinct in the ledger and the per-submission surfaces still label them by
-- actor; only the profile-level total adds them up.
--
-- The join is over the event ledger, so a collective holding nothing but
-- submissions still awaiting review IS listed, with approved_count = 0. The
-- counters do the filtering; the join must never do it.
--
-- Owner-only: $1 is the authenticated caller. There is deliberately no username
-- parameter and no username route, so no viewer can ask for another person's
-- contributions. That is also why this statement carries no collective
-- visibility predicate - the only thing such a predicate could hide is a
-- collective the caller contributed to themselves.
-- $1 = owner_id.
SELECT g.id, g.name, g.description, g.linked_github_org,
       COUNT(DISTINCT CASE WHEN ts.status = 'approved' THEN ts.transcript_id END)::int AS approved_count,
       COUNT(DISTINCT CASE WHEN ts.status = 'pending'  THEN ts.transcript_id END)::int AS pending_count,
       COUNT(CASE WHEN a.status = 'rejected' THEN 1 END)::int AS rejected_attempt_count,
       COUNT(CASE WHEN a.status IN ('retracted', 'revoked') THEN 1 END)::int AS withdrawn_attempt_count
FROM transcript_share_attempts a
JOIN transcripts t ON t.id = a.transcript_id
JOIN groups g ON g.id = a.group_id
LEFT JOIN transcript_shares ts
       ON ts.transcript_id = a.transcript_id AND ts.group_id = a.group_id
WHERE t.owner_id = $1
GROUP BY g.id
ORDER BY approved_count DESC, pending_count DESC, g.name, g.id;

-- name: ListProjectCollectiveRollup :many
-- The collectives holding any accepted transcript of one project, for the
-- project page. ONE aggregate over every transcript of the (owner, project)
-- pair, grouped by collective - never a query per transcript.
--
-- Two independent gates apply, and both must hold:
--   - the collective visibility rule, character-identical to SearchCollectives,
--     so a viewer never learns of a members-only collective they are outside;
--   - the contributor opt-in, the same predicate ListGroupContributors applies,
--     so a person who has not opted in to being listed as a contributor is not
--     listed by this surface either. When they have not opted in the result is
--     an EMPTY LIST rather than a refusal, because a refusal would itself
--     confirm that hidden memberships exist.
-- $1 = owner_id, $2 = project_hash, $3 = viewer user_id (NULL when anonymous),
-- viewer_is_owner = whether the viewer IS the project owner.
SELECT g.id, g.name, g.description, g.linked_github_org,
       COUNT(DISTINCT ts.transcript_id)::int AS transcript_count
FROM transcripts t
JOIN transcript_shares ts ON ts.transcript_id = t.id AND ts.status = 'approved'
JOIN groups g ON g.id = ts.group_id
JOIN users u ON u.id = t.owner_id
WHERE t.owner_id = $1
  AND t.project_hash = $2
  AND (u.is_discoverable = TRUE OR @viewer_is_owner::boolean)
  AND (
        g.data_access = 'public'
        OR g.acceptance_mode = 'open'
        OR EXISTS (
            SELECT 1 FROM group_members gm2
            WHERE gm2.group_id = g.id AND gm2.user_id = $3
        )
      )
GROUP BY g.id
ORDER BY transcript_count DESC, g.name, g.id;

-- name: ListTranscriptCollectivesForViewer :many
-- The accepted memberships of one transcript, restricted to the collectives the
-- viewer may see. The visibility predicate is character-identical to
-- SearchCollectives and is proven equivalent to it by a shared corpus, not by
-- reading the two side by side.
--
-- The contributor opt-in gates this surface too: the memberships are returned
-- only when the transcript owner has opted in to being listed, or the viewer IS
-- that owner. Otherwise the answer is an EMPTY LIST - never a refusal, which
-- would itself confirm that hidden memberships exist.
-- $1 = transcript_id, $2 = viewer user_id (NULL when anonymous),
-- viewer_is_owner = whether the viewer IS the transcript owner.
SELECT g.id, g.name, g.description, g.linked_github_org, ts.shared_at
FROM transcript_shares ts
JOIN groups g ON g.id = ts.group_id
JOIN transcripts t ON t.id = ts.transcript_id
JOIN users u ON u.id = t.owner_id
WHERE ts.transcript_id = $1
  AND ts.status = 'approved'
  AND (u.is_discoverable = TRUE OR @viewer_is_owner::boolean)
  AND (
        g.data_access = 'public'
        OR g.acceptance_mode = 'open'
        OR EXISTS (
            SELECT 1 FROM group_members gm2
            WHERE gm2.group_id = g.id AND gm2.user_id = $2
        )
      )
ORDER BY g.name, g.id;
