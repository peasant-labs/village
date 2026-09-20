-- Query projections of the validated durable detail. Historical absence is not
-- a measured zero and must never be reconstructed from transcript turn totals.
ALTER TABLE transcripts
    ADD COLUMN input_submission_count BIGINT,
    ADD COLUMN root_session_id TEXT,
    ADD COLUMN session_purpose TEXT,
    ADD COLUMN session_relationships JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD CONSTRAINT transcripts_input_submission_count_range
        CHECK (input_submission_count BETWEEN 0 AND 9007199254740991),
    ADD CONSTRAINT transcripts_root_session_id_nonempty
        CHECK (root_session_id IS NULL OR length(root_session_id) > 0),
    ADD CONSTRAINT transcripts_session_purpose_menu
        CHECK (session_purpose IN ('interaction', 'delegated_work', 'helper_review', 'unknown')),
    ADD CONSTRAINT transcripts_session_relationships_array
        CHECK (jsonb_typeof(session_relationships) = 'array'
            AND NOT jsonb_path_exists(session_relationships, '$[*] ? (@.type() != "object")'));

-- Targets are owner-local evidence, not foreign keys: publishing the child
-- before its parent must preserve the target without granting read access.
CREATE INDEX idx_transcripts_owner_root_session
    ON transcripts (owner_id, root_session_id)
    WHERE root_session_id IS NOT NULL;
