-- name: UpsertGitHubAppInstallation :exec
-- Records (or refreshes) a known GitHub App installation. Idempotent on
-- installation_id so re-observing the same installation just updates metadata.
INSERT INTO github_app_installations (
    installation_id, account_login, account_id, account_type
) VALUES (
    $1, $2, $3, $4
)
ON CONFLICT (installation_id) DO UPDATE SET
    account_login = EXCLUDED.account_login,
    account_id    = EXCLUDED.account_id,
    account_type  = EXCLUDED.account_type,
    updated_at    = now();

-- name: GetGitHubAppInstallation :one
SELECT * FROM github_app_installations WHERE installation_id = $1;

-- name: LinkCollectiveRepository :one
-- Links a repo to a collective. UNIQUE(group_id, owner, name) means a repeat
-- link is a no-op-ish upsert that refreshes the installation/privacy/linker.
INSERT INTO collective_repositories (
    group_id, owner, name, installation_id, is_private, linked_by
) VALUES (
    $1, $2, $3, $4, $5, $6
)
ON CONFLICT (group_id, owner, name) DO UPDATE SET
    installation_id = EXCLUDED.installation_id,
    is_private      = EXCLUDED.is_private,
    linked_by       = EXCLUDED.linked_by
RETURNING *;

-- name: UnlinkCollectiveRepository :execrows
-- Removes a repo link from a collective. Returns rows affected so the handler
-- can distinguish "unlinked" from "was not linked" (404).
DELETE FROM collective_repositories
WHERE group_id = $1 AND lower(owner) = lower($2) AND lower(name) = lower($3);

-- name: ListCollectiveRepositories :many
SELECT * FROM collective_repositories
WHERE group_id = $1
ORDER BY owner ASC, name ASC;

-- name: GetCollectiveRepository :one
SELECT * FROM collective_repositories
WHERE group_id = $1 AND lower(owner) = lower($2) AND lower(name) = lower($3);

-- name: UpdateCollectiveRepositorySync :exec
-- Persists the latest ETag + sync time after a commit refresh so the next
-- refresh can issue a conditional request.
UPDATE collective_repositories
SET commits_etag = $4, last_synced_at = now()
WHERE group_id = $1 AND lower(owner) = lower($2) AND lower(name) = lower($3);

-- name: UpsertRepositoryCommit :exec
-- Caches a single repo commit. Idempotent on (owner, name, sha): re-fetching the
-- same commit refreshes its row rather than duplicating it.
INSERT INTO repository_commits (
    owner, name, sha, message, author_name, author_email, authored_at, committed_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
ON CONFLICT (owner, name, sha) DO UPDATE SET
    message      = EXCLUDED.message,
    author_name  = EXCLUDED.author_name,
    author_email = EXCLUDED.author_email,
    authored_at  = EXCLUDED.authored_at,
    committed_at = EXCLUDED.committed_at,
    fetched_at   = now();

-- name: ListRepositoryCommits :many
-- Returns cached commits for a repo, newest first.
SELECT * FROM repository_commits
WHERE lower(owner) = lower($1) AND lower(name) = lower($2)
ORDER BY committed_at DESC NULLS LAST, sha ASC
LIMIT $3;

-- name: CountRepositoryCommits :one
SELECT count(*) FROM repository_commits
WHERE lower(owner) = lower($1) AND lower(name) = lower($2);
