-- GitHub App installations the platform knows about. One row per installation
-- of the village GitHub App on a user/org account. installation_id is the
-- numeric ID GitHub assigns; we mint installation tokens against it.
CREATE TABLE github_app_installations (
    installation_id BIGINT PRIMARY KEY,        -- GitHub installation ID
    account_login   TEXT NOT NULL,             -- org/user login the App is installed on
    account_id      BIGINT NOT NULL,           -- GitHub account (org/user) numeric ID
    account_type    TEXT,                      -- "Organization" | "User"
    created_at      TIMESTAMPTZ DEFAULT now(),
    updated_at      TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_github_app_installations_login ON github_app_installations(lower(account_login));

-- Repositories a collective has linked. Only collective owners may link/unlink.
-- The installation_id ties the repo to the App installation we fetch commits
-- through. UNIQUE(group_id, owner, name) prevents linking the same repo twice.
CREATE TABLE collective_repositories (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id        UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    owner           TEXT NOT NULL,             -- repo owner login
    name            TEXT NOT NULL,             -- repo name
    installation_id BIGINT NOT NULL,           -- App installation used to reach it
    is_private      BOOLEAN NOT NULL DEFAULT false,
    linked_by       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- commits_etag stores the last ETag from the commits endpoint so subsequent
    -- refreshes can send a conditional (If-None-Match) request.
    commits_etag    TEXT,
    last_synced_at  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ DEFAULT now(),
    UNIQUE (group_id, owner, name)
);

CREATE INDEX idx_collective_repositories_group ON collective_repositories(group_id);
CREATE INDEX idx_collective_repositories_repo ON collective_repositories(lower(owner), lower(name));
