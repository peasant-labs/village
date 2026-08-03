-- name: UpsertUserGitHubOrg :exec
INSERT INTO user_github_orgs (user_id, org_login, org_id, avatar_url, fetched_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (user_id, org_login) DO UPDATE
SET org_id = EXCLUDED.org_id,
    avatar_url = EXCLUDED.avatar_url,
    fetched_at = now();

-- name: DeleteStaleUserOrgs :exec
DELETE FROM user_github_orgs
WHERE user_id = $1 AND fetched_at < $2;

-- name: ListUserVisibleOrgs :many
SELECT org_login, org_id, avatar_url, visible, fetched_at
FROM user_github_orgs
WHERE user_id = $1 AND visible = true
ORDER BY org_login;

-- name: HasUserVisibleOrg :one
-- Returns true if the user has the given org_login marked visible=true.
-- Used to validate "linked_github_org" claims and the verified_only org-specific gate.
SELECT EXISTS (
    SELECT 1 FROM user_github_orgs
    WHERE user_id = $1
      AND lower(org_login) = lower($2)
      AND visible = true
) AS has_org;

-- name: ListUserAllOrgs :many
SELECT org_login, org_id, avatar_url, visible, fetched_at
FROM user_github_orgs
WHERE user_id = $1
ORDER BY org_login;

-- name: SetOrgVisibility :exec
UPDATE user_github_orgs
SET visible = $3
WHERE user_id = $1 AND org_login = $2;

-- name: GetUserVisibleOrgsByUsername :many
SELECT ugo.org_login, ugo.avatar_url
FROM user_github_orgs ugo
JOIN users u ON ugo.user_id = u.id
WHERE lower(u.github_username) = lower($1) AND ugo.visible = true
ORDER BY ugo.org_login;

-- name: ListVisibleOrgsByUserIDs :many
SELECT ugo.user_id, ugo.org_login, ugo.avatar_url
FROM user_github_orgs ugo
WHERE ugo.user_id = ANY($1::uuid[]) AND ugo.visible = true
ORDER BY ugo.org_login;

-- name: SearchOrgs :many
SELECT
    ugo.org_login,
    MAX(ugo.avatar_url) AS avatar_url,
    COUNT(DISTINCT ugo.user_id)::int AS member_count,
    COUNT(DISTINCT t.id)::int AS transcript_count
FROM user_github_orgs ugo
LEFT JOIN transcripts t
    ON t.owner_id = ugo.user_id AND t.visibility = 'public'
WHERE ugo.visible = true
  AND ugo.org_login ILIKE '%' || $1 || '%'
GROUP BY ugo.org_login
ORDER BY member_count DESC, transcript_count DESC
LIMIT $2;

-- name: GetOrgStats :one
SELECT
    ugo.org_login,
    MAX(ugo.avatar_url) AS avatar_url,
    COUNT(DISTINCT ugo.user_id)::int AS member_count,
    COUNT(DISTINCT t.id)::int AS transcript_count
FROM user_github_orgs ugo
LEFT JOIN transcripts t
    ON t.owner_id = ugo.user_id AND t.visibility = 'public'
WHERE ugo.visible = true
  AND lower(ugo.org_login) = lower($1)
GROUP BY ugo.org_login;

-- name: ListOrgMembers :many
SELECT
    u.id,
    u.github_username,
    u.display_name,
    u.avatar_url,
    COUNT(DISTINCT t.id)::int AS transcript_count
FROM user_github_orgs ugo
JOIN users u ON ugo.user_id = u.id
LEFT JOIN transcripts t
    ON t.owner_id = u.id AND t.visibility = 'public'
WHERE ugo.visible = true
  AND lower(ugo.org_login) = lower($1)
GROUP BY u.id, u.github_username, u.display_name, u.avatar_url
ORDER BY transcript_count DESC, u.github_username;
