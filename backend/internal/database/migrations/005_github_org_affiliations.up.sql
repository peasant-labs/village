-- GitHub org affiliations for trust/signalling layer
CREATE TABLE user_github_orgs (
    user_id     UUID REFERENCES users(id) ON DELETE CASCADE,
    org_login   VARCHAR(255) NOT NULL,
    org_id      BIGINT NOT NULL,
    avatar_url  TEXT,
    visible     BOOLEAN NOT NULL DEFAULT false,
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, org_login)
);
CREATE INDEX idx_user_github_orgs_user ON user_github_orgs(user_id);
CREATE INDEX idx_user_github_orgs_org ON user_github_orgs(org_login);

-- Collective acceptance modes
ALTER TABLE groups ADD COLUMN acceptance_mode VARCHAR(20) NOT NULL DEFAULT 'open'
    CHECK (acceptance_mode IN ('open', 'verified_only', 'curated'));

-- Track approval status for curated collectives
ALTER TABLE transcript_shares ADD COLUMN status VARCHAR(20) NOT NULL DEFAULT 'approved'
    CHECK (status IN ('pending', 'approved', 'rejected'));
CREATE INDEX idx_transcript_shares_status ON transcript_shares(status);
