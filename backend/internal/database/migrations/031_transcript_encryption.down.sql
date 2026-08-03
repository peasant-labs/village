LOCK TABLE transcripts IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM transcripts LIMIT 1) THEN
        RAISE EXCEPTION USING
            ERRCODE = 'object_not_in_prerequisite_state',
            MESSAGE = 'transcript encryption migration cannot be reversed while transcripts exist',
            DETAIL = 'Removing encryption descriptors from live rows would make their ciphertext unreadable.',
            HINT = 'Stop rollback, preserve required data, and use the approved empty-database rollback procedure before retrying.';
    END IF;
END
$$;

DROP TRIGGER IF EXISTS trg_transcript_writer_version ON transcripts;
DROP FUNCTION IF EXISTS enforce_transcript_writer_version();
DROP INDEX IF EXISTS idx_transcripts_key_version;
ALTER TABLE transcripts
    DROP CONSTRAINT IF EXISTS transcripts_key_version_positive,
    DROP CONSTRAINT IF EXISTS transcripts_encryption_algorithm_valid,
    DROP CONSTRAINT IF EXISTS transcripts_wrapped_data_key_nonempty,
    DROP COLUMN IF EXISTS key_version,
    DROP COLUMN IF EXISTS encryption_algorithm,
    DROP COLUMN IF EXISTS wrapped_data_key;
