DROP INDEX idx_transcripts_owner_root_session;
ALTER TABLE transcripts
    DROP COLUMN input_submission_count,
    DROP COLUMN root_session_id,
    DROP COLUMN session_purpose,
    DROP COLUMN session_relationships;
