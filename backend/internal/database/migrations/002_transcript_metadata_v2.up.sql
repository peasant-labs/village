-- New columns for the v2 transcript metadata schema from local transcript store

ALTER TABLE transcripts
    ADD COLUMN parent_session_id TEXT,
    ADD COLUMN ingested_at TIMESTAMPTZ,
    ADD COLUMN source_file_path TEXT,
    ADD COLUMN source_format VARCHAR(20),
    ADD COLUMN git_branch VARCHAR(255),
    ADD COLUMN git_remote TEXT,
    ADD COLUMN git_worktree TEXT,
    ADD COLUMN project_hash VARCHAR(128),
    ADD COLUMN project_path TEXT,
    ADD COLUMN project_name VARCHAR(255),
    ADD COLUMN tool_call_count INTEGER,
    ADD COLUMN subagent_count INTEGER,
    ADD COLUMN duration_ms BIGINT,
    ADD COLUMN subagents JSONB,
    ADD COLUMN diagnostics_warnings JSONB,
    ADD COLUMN diagnostics_partial BOOLEAN;

CREATE INDEX idx_transcripts_project_name ON transcripts(project_name);
CREATE INDEX idx_transcripts_git_remote ON transcripts(git_remote);
CREATE INDEX idx_transcripts_duration ON transcripts(duration_ms);
