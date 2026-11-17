-- Revision and authenticated identity for partitioned reads. Existing rows
-- start at one without reading or rewriting their encrypted objects.
ALTER TABLE transcripts
    ADD COLUMN content_revision BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN segmented_identity_hash TEXT,
    ADD COLUMN segmented_identity_size BIGINT,
    ADD CONSTRAINT transcripts_content_revision_positive CHECK (content_revision >= 1),
    ADD CONSTRAINT transcripts_content_revision_identity CHECK (
        content_revision = 1
        OR (content_hash IS NOT NULL AND content_hash ~ '^[0-9a-f]{64}$'
            AND blob_size_bytes IS NOT NULL AND blob_size_bytes >= 0)
        OR (content_hash IS NULL
            AND segmented_identity_hash IS NOT NULL
            AND segmented_identity_hash ~ '^[0-9a-f]{64}$'
            AND segmented_identity_size IS NOT NULL
            AND segmented_identity_size >= 0)),
    ADD CONSTRAINT transcripts_segmented_identity_paired CHECK (
        (segmented_identity_hash IS NULL AND segmented_identity_size IS NULL)
        OR (segmented_identity_hash IS NOT NULL AND segmented_identity_size IS NOT NULL)),
    ADD CONSTRAINT transcripts_segmented_identity_hash_shape CHECK (
        segmented_identity_hash IS NULL OR segmented_identity_hash ~ '^[0-9a-f]{64}$'),
    ADD CONSTRAINT transcripts_segmented_identity_size_nonnegative CHECK (
        segmented_identity_size IS NULL OR segmented_identity_size >= 0),
    ADD CONSTRAINT transcripts_identity_slot_exclusive CHECK (
        content_hash IS NULL OR segmented_identity_hash IS NULL);
