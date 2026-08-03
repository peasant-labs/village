-- ⚠️ DO-NOT-TOUCH (TRAP — NOT the harness wire key): users.provider here is the
-- OAuth AUTH IDENTITY (github/gitlab/...), unrelated to the transcript harness.
-- It must NEVER be flipped to `harness` during the push-wire provider→harness
-- changeover. (users.sql.go is sqlc-GENERATED from this file — change neither.)

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByUsername :one
-- Canonical handle lookup. github_username is globally unique (case-insensitive),
-- so this resolves to exactly one user.
SELECT * FROM users WHERE lower(github_username) = lower($1);

-- name: UpsertUser :one
-- github_username here is a freshly-generated unique candidate handle used only
-- on INSERT; on a returning user (conflict) we preserve their chosen handle and
-- only refresh the raw provider handle / profile fields.
INSERT INTO users (github_id, github_username, provider_username, display_name, avatar_url, provider, provider_user_id, username_chosen)
VALUES ($1, $2, $3, $4, $5, 'github', $1::bigint::text, false)
ON CONFLICT (github_id) DO UPDATE SET
    provider_username = EXCLUDED.provider_username,
    display_name = EXCLUDED.display_name,
    avatar_url = EXCLUDED.avatar_url,
    updated_at = now()
RETURNING *;

-- name: UpsertUserByProvider :one
-- Upsert a user identified by (provider, provider_user_id). github_id is a
-- synthesized stable BIGINT (caller computes from sha256 of the external id);
-- it is not a real GitHub ID for non-github providers. github_username is the
-- canonical, user-chosen handle: on INSERT it's a generated unique candidate,
-- and on a returning user (conflict) it is preserved — only provider_username
-- and profile fields are refreshed.
INSERT INTO users (github_id, github_username, provider_username, display_name, avatar_url, provider, provider_user_id, username_chosen)
VALUES ($1, $2, $3, $4, $5, $6, $7, false)
ON CONFLICT (provider, provider_user_id) DO UPDATE SET
    provider_username = EXCLUDED.provider_username,
    display_name = EXCLUDED.display_name,
    avatar_url = EXCLUDED.avatar_url,
    updated_at = now()
RETURNING *;

-- name: SetUsername :one
-- Sets the user's canonical handle and marks it explicitly chosen. Relies on the
-- case-insensitive unique index to reject collisions (caller maps 23505 -> 409).
UPDATE users SET
    github_username = $2,
    username_chosen = true,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: UpdateUserDiscoverable :one
UPDATE users SET
    is_discoverable = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;
