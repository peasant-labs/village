-- Cache of commits fetched from a linked repository's GitHub history.
-- Keyed by (owner, name, sha) so the same repo linked by multiple collectives
-- shares one cache, and so the transcript<->commit overlay can join transcript
-- commit SHAs (transcript_commits.sha) against a repo's real commit timeline.
CREATE TABLE repository_commits (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner         TEXT NOT NULL,              -- repo owner login
    name          TEXT NOT NULL,              -- repo name
    sha           TEXT NOT NULL,              -- commit SHA-1
    message       TEXT,                       -- full commit message
    author_name   TEXT,
    author_email  TEXT,
    authored_at   TIMESTAMPTZ,                -- author date
    committed_at  TIMESTAMPTZ,                -- committer date
    fetched_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner, name, sha)
);

-- Lookup by repo (timeline view) and by SHA (overlay join with transcript_commits).
CREATE INDEX idx_repository_commits_repo ON repository_commits(lower(owner), lower(name));
CREATE INDEX idx_repository_commits_sha ON repository_commits(sha);
