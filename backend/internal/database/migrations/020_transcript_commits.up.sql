-- Per-transcript git commits produced during a session.
-- Populated from the peasant publish payload's gitContext.commits[].
-- Persisting commit SHAs lets us later link a transcript to a repo's commits
-- (the "SHA bridge", Tier 2).
CREATE TABLE transcript_commits (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transcript_id UUID REFERENCES transcripts(id) ON DELETE CASCADE NOT NULL,
    commit_order  INTEGER NOT NULL,           -- order within the payload's commits[]
    sha           TEXT NOT NULL,              -- commit SHA-1 (full or abbreviated)
    message       TEXT,                       -- commit message first line
    author_name   TEXT,                       -- author display name
    author_email  TEXT,                       -- author email
    additions     INTEGER,                    -- inserted lines (nullable; not yet in payload)
    deletions     INTEGER,                    -- deleted lines (nullable; not yet in payload)
    authored_at   TIMESTAMPTZ,                -- author date
    committed_at  TIMESTAMPTZ,                -- committer date
    created_at    TIMESTAMPTZ DEFAULT now(),
    UNIQUE (transcript_id, sha)
);

CREATE INDEX idx_transcript_commits_transcript ON transcript_commits(transcript_id);
CREATE INDEX idx_transcript_commits_sha ON transcript_commits(sha);
