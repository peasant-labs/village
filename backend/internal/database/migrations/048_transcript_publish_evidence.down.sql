-- Forward-only in any shared environment: use this only on a development
-- database. It discards every stored metadata object reference and every
-- rewrite record.
DROP TABLE IF EXISTS transcript_content_rewrites;

DROP TRIGGER IF EXISTS trg_transcript_writer_version ON transcripts;
CREATE TRIGGER trg_transcript_writer_version
BEFORE INSERT OR DELETE OR UPDATE OF blob_key, wrapped_data_key, encryption_algorithm, key_version, content_hash, blob_size_bytes
ON transcripts
FOR EACH ROW
EXECUTE FUNCTION enforce_transcript_writer_version();

ALTER TABLE transcripts
    DROP CONSTRAINT IF EXISTS transcripts_publish_metadata_size_nonnegative,
    DROP CONSTRAINT IF EXISTS transcripts_publish_metadata_hash_shape,
    DROP CONSTRAINT IF EXISTS transcripts_publish_metadata_key_not_blob_key,
    DROP CONSTRAINT IF EXISTS transcripts_publish_metadata_key_shape,
    DROP CONSTRAINT IF EXISTS transcripts_publish_metadata_complete,
    DROP COLUMN IF EXISTS publish_metadata_size,
    DROP COLUMN IF EXISTS publish_metadata_hash,
    DROP COLUMN IF EXISTS publish_metadata_key;
