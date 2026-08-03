ALTER TABLE annotations
    DROP CONSTRAINT IF EXISTS annotations_owner_content_hash_key;

ALTER TABLE annotations
    ADD CONSTRAINT annotations_content_hash_key
    UNIQUE (content_hash);
