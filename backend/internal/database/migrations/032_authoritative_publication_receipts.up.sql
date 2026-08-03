ALTER TABLE transcripts
    ADD COLUMN accepted_request_operation_fingerprint TEXT;

ALTER TABLE transcripts
    ADD CONSTRAINT transcripts_accepted_request_operation_fingerprint_shape
    CHECK (
        accepted_request_operation_fingerprint IS NULL OR
        accepted_request_operation_fingerprint ~ '^[0-9a-f]{64}$'
    );
