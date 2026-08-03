-- name: CreateAPIKey :one
INSERT INTO api_keys (user_id, key_hash, key_prefix, label)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAPIKeyByHash :one
SELECT ak.*, u.id as uid, u.github_id, u.github_username, u.display_name, u.avatar_url
FROM api_keys ak
JOIN users u ON ak.user_id = u.id
WHERE ak.key_hash = $1 AND ak.revoked_at IS NULL;

-- name: ListUserAPIKeys :many
SELECT id, key_prefix, label, created_at, last_used, revoked_at
FROM api_keys
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: RevokeAPIKey :exec
UPDATE api_keys SET revoked_at = now() WHERE id = $1 AND user_id = $2;

-- name: TouchAPIKeyLastUsed :exec
UPDATE api_keys SET last_used = now() WHERE id = $1;
