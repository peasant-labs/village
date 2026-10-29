-- The validated publish metadata is stored as a second encrypted object next
-- to the transcript content. These three columns describe that object the way
-- blob_key, content_hash and blob_size_bytes describe the content: the hash and
-- size authenticate the decrypted object before anything reads it, so neither
-- can be derived without decrypting the object it validates. The client name
-- and version stay inside the stored object; no query filters on them.
ALTER TABLE transcripts
    ADD COLUMN publish_metadata_key TEXT,
    ADD COLUMN publish_metadata_hash TEXT,
    ADD COLUMN publish_metadata_size BIGINT,
    ADD CONSTRAINT transcripts_publish_metadata_complete CHECK (
        (publish_metadata_key IS NULL AND publish_metadata_hash IS NULL AND publish_metadata_size IS NULL)
        OR (publish_metadata_key IS NOT NULL AND publish_metadata_hash IS NOT NULL AND publish_metadata_size IS NOT NULL)
    ),
    ADD CONSTRAINT transcripts_publish_metadata_key_shape CHECK (publish_metadata_key ~ '^transcripts/[0-9a-f-]{36}\.bin$'),
    ADD CONSTRAINT transcripts_publish_metadata_hash_shape CHECK (publish_metadata_hash ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT transcripts_publish_metadata_size_nonnegative CHECK (publish_metadata_size >= 0);

-- The writer fence now also covers the metadata object, so a writer that does
-- not know about it cannot move it.
DROP TRIGGER trg_transcript_writer_version ON transcripts;
CREATE TRIGGER trg_transcript_writer_version
BEFORE INSERT OR DELETE OR UPDATE OF blob_key, wrapped_data_key, encryption_algorithm, key_version, content_hash, blob_size_bytes, publish_metadata_key, publish_metadata_hash, publish_metadata_size
ON transcripts
FOR EACH ROW
EXECUTE FUNCTION enforce_transcript_writer_version();

-- One row per time Village replaced a transcript's stored content with its own
-- rewrite. Whether the replaced content was legacy JSONL is not derivable once
-- the rewrite has replaced it, so the raw fact is kept. Lineage is derived by a
-- recursive query, never stored.
CREATE TABLE transcript_content_rewrites (
    transcript_id         UUID        NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    replaced_content_hash TEXT        NOT NULL,
    installed_content_hash TEXT       NOT NULL,
    replaced_was_legacy   BOOLEAN     NOT NULL,
    rewritten_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (transcript_id, installed_content_hash),
    CONSTRAINT transcript_content_rewrites_replaced_hash_shape CHECK (replaced_content_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT transcript_content_rewrites_installed_hash_shape CHECK (installed_content_hash ~ '^[0-9a-f]{64}$')
);
