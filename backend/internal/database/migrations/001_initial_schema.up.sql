-- Users (populated from GitHub OAuth)
CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    github_id       BIGINT UNIQUE NOT NULL,
    github_username VARCHAR(255) NOT NULL,
    display_name    VARCHAR(255),
    avatar_url      TEXT,
    created_at      TIMESTAMPTZ DEFAULT now(),
    updated_at      TIMESTAMPTZ DEFAULT now()
);

-- Groups (data collectives)
CREATE TABLE groups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255) NOT NULL,
    description TEXT,
    created_by  UUID REFERENCES users(id) NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT now(),
    updated_at  TIMESTAMPTZ DEFAULT now()
);

-- Group membership
CREATE TABLE group_members (
    group_id    UUID REFERENCES groups(id) ON DELETE CASCADE,
    user_id     UUID REFERENCES users(id) ON DELETE CASCADE,
    role        VARCHAR(20) NOT NULL CHECK (role IN ('owner', 'member')),
    joined_at   TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (group_id, user_id)
);

-- Transcripts (published copies from users' local backends)
CREATE TABLE transcripts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id        UUID REFERENCES users(id) NOT NULL,
    local_id        TEXT NOT NULL,
    title           VARCHAR(500),
    description     TEXT,
    visibility      VARCHAR(20) NOT NULL DEFAULT 'private'
                        CHECK (visibility IN ('public', 'private', 'shared')),
    model_provider  VARCHAR(50) NOT NULL,
    model_name      VARCHAR(100),
    harness_version VARCHAR(50),
    session_start   TIMESTAMPTZ,
    session_end     TIMESTAMPTZ,
    turn_count      INTEGER,
    token_count     INTEGER,
    blob_key        TEXT NOT NULL,
    blob_size_bytes BIGINT,
    schema_version  VARCHAR(20) NOT NULL,
    published_at    TIMESTAMPTZ DEFAULT now(),
    updated_at      TIMESTAMPTZ DEFAULT now(),
    UNIQUE (owner_id, local_id)
);

-- Tags
CREATE TABLE tags (
    id      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name    VARCHAR(100) UNIQUE NOT NULL
);

CREATE TABLE transcript_tags (
    transcript_id   UUID REFERENCES transcripts(id) ON DELETE CASCADE,
    tag_id          UUID REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (transcript_id, tag_id)
);

-- Which groups a transcript is shared with
CREATE TABLE transcript_shares (
    transcript_id   UUID REFERENCES transcripts(id) ON DELETE CASCADE,
    group_id        UUID REFERENCES groups(id) ON DELETE CASCADE,
    shared_at       TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (transcript_id, group_id)
);

-- API keys for CLI authentication
CREATE TABLE api_keys (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID REFERENCES users(id) ON DELETE CASCADE NOT NULL,
    key_hash   TEXT NOT NULL,
    key_prefix VARCHAR(8) NOT NULL,
    label      VARCHAR(255),
    created_at TIMESTAMPTZ DEFAULT now(),
    last_used  TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

-- Indexes
CREATE INDEX idx_transcripts_owner ON transcripts(owner_id);
CREATE INDEX idx_transcripts_visibility ON transcripts(visibility);
CREATE INDEX idx_transcripts_provider ON transcripts(model_provider);
CREATE INDEX idx_transcripts_published ON transcripts(published_at DESC);
CREATE INDEX idx_group_members_user ON group_members(user_id);
CREATE INDEX idx_transcript_shares_group ON transcript_shares(group_id);
CREATE INDEX idx_api_keys_hash ON api_keys(key_hash);
CREATE INDEX idx_api_keys_user ON api_keys(user_id);
