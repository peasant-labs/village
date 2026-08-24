ALTER TABLE transcripts
    DROP CONSTRAINT IF EXISTS transcripts_session_origin_menu;

ALTER TABLE transcripts
    DROP COLUMN IF EXISTS session_origin;
