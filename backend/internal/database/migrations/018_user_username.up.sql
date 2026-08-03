-- github_username becomes the canonical, user-chosen, globally-unique handle.
-- Multi-provider signups can collide on the same handle, so we (a) keep the raw
-- provider handle separately for suggestions/provenance, (b) track whether the
-- user has explicitly chosen their handle so the app can prompt after SSO
-- signup, and (c) resolve existing collisions before enforcing uniqueness.

ALTER TABLE users ADD COLUMN username_chosen BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN provider_username TEXT;

-- Preserve each user's raw provider handle; existing users keep their current
-- handle and are treated as already-chosen.
UPDATE users SET provider_username = github_username;
UPDATE users SET username_chosen = true;

-- Resolve handle collisions: keep the earliest row's handle, suffix the rest
-- with their provider and force them to re-choose on next visit.
WITH ranked AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY lower(github_username)
               ORDER BY created_at, id
           ) AS rn
    FROM users
)
UPDATE users u
SET github_username = u.github_username || '-' || u.provider,
    username_chosen  = false
FROM ranked r
WHERE u.id = r.id AND r.rn > 1;

-- Case-insensitive uniqueness on the canonical handle.
CREATE UNIQUE INDEX idx_users_github_username_lower ON users (lower(github_username));
