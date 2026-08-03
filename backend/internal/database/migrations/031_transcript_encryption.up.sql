LOCK TABLE transcripts IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM transcripts LIMIT 1) THEN
        RAISE EXCEPTION USING
            ERRCODE = 'object_not_in_prerequisite_state',
            MESSAGE = 'transcript encryption migration cannot activate on a non-empty transcripts table',
            DETAIL = 'Migration 031 requires a clean database because existing objects have no authenticated encryption descriptor.',
            HINT = 'Stop migration, verify that this is the intended reset database, preserve any user data that must survive, empty the table through the approved cutover, and retry.';
    END IF;
END
$$;

ALTER TABLE transcripts
    ADD COLUMN wrapped_data_key BYTEA NOT NULL,
    ADD COLUMN encryption_algorithm TEXT NOT NULL,
    ADD COLUMN key_version INTEGER NOT NULL,
    ADD CONSTRAINT transcripts_wrapped_data_key_nonempty CHECK (octet_length(wrapped_data_key) > 0),
    ADD CONSTRAINT transcripts_encryption_algorithm_valid CHECK (encryption_algorithm = 'aes-256-gcm-random-nonce-v1'),
    ADD CONSTRAINT transcripts_key_version_positive CHECK (key_version > 0);

CREATE INDEX idx_transcripts_key_version ON transcripts (key_version);

CREATE FUNCTION enforce_transcript_writer_version()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, public
AS $$
DECLARE
    writer_version TEXT := NULLIF(current_setting('app.transcript_writer_version', true), '');
BEGIN
    IF writer_version IS DISTINCT FROM '1' THEN
        RAISE EXCEPTION USING
            ERRCODE = 'object_not_in_prerequisite_state',
            MESSAGE = 'transcript storage mutation rejected because the encryption writer marker is missing or unsupported',
            DETAIL = format('Operation %s reached the migration 031 compatibility fence without app.transcript_writer_version=1.', TG_OP),
            HINT = 'Use an encryption-capable Village writer and set the transaction-local writer marker before mutating transcript storage state.';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER trg_transcript_writer_version
BEFORE INSERT OR DELETE OR UPDATE OF blob_key, wrapped_data_key, encryption_algorithm, key_version, content_hash, blob_size_bytes
ON transcripts
FOR EACH ROW
EXECUTE FUNCTION enforce_transcript_writer_version();
