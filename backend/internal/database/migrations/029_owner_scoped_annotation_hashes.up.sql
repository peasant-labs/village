-- Annotation hashes describe a producer's canonical annotation payload, not a
-- Village-global object. Different owners can legitimately emit identical
-- payloads, so identity must include the owning account.
ALTER TABLE annotations
    DROP CONSTRAINT IF EXISTS annotations_content_hash_key;

ALTER TABLE annotations
    ADD CONSTRAINT annotations_owner_content_hash_key
    UNIQUE (owner_id, content_hash);
