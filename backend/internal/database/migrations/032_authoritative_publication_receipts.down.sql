ALTER TABLE transcripts
    DROP CONSTRAINT IF EXISTS transcripts_accepted_request_operation_fingerprint_shape;

ALTER TABLE transcripts
    DROP COLUMN IF EXISTS accepted_request_operation_fingerprint;
