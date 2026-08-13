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
-- This per-surface split is intentional and user-ratified (Plan UAT) — do
-- not "reconcile" the two by changing either side.
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
