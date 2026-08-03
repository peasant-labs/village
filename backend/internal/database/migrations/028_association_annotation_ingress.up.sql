-- Durable, producer-owned session-to-commit relationships. An association ID is
-- scoped to its owner; the relationship key additionally prevents a second ID
-- from aliasing the same owner/transcript/observed-commit binding.
ALTER TABLE transcripts
    ADD CONSTRAINT transcripts_owner_id_id_key UNIQUE (owner_id, id);

CREATE TABLE transcript_associations (
    owner_id            UUID NOT NULL,
    association_id      TEXT NOT NULL,
    transcript_id       UUID NOT NULL,
    observed_commit_sha TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT transcript_associations_pkey PRIMARY KEY (owner_id, association_id),
    CONSTRAINT transcript_associations_owner_transcript_fk
        FOREIGN KEY (owner_id, transcript_id)
        REFERENCES transcripts (owner_id, id)
        ON DELETE CASCADE,
    CONSTRAINT transcript_associations_relationship_key
        UNIQUE (owner_id, transcript_id, observed_commit_sha),
    CONSTRAINT transcript_associations_id_shape
        CHECK (association_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
    CONSTRAINT transcript_associations_observed_commit_not_blank
        CHECK (btrim(observed_commit_sha) <> '')
);

CREATE INDEX idx_transcript_associations_transcript
    ON transcript_associations (transcript_id);

-- Associations are an append-only identity ledger. Rows may disappear only via
-- their transcript/user cascade; rebinding an existing ID is rejected at the
-- database boundary even if a future application path bypasses the handler.
CREATE FUNCTION prevent_transcript_association_update()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, public
AS $$
BEGIN
    RAISE EXCEPTION 'transcript associations are append-only; publish a new association instead of rebinding an existing one';
END;
$$;

CREATE TRIGGER trg_transcript_associations_immutable
BEFORE UPDATE ON transcript_associations
FOR EACH ROW EXECUTE FUNCTION prevent_transcript_association_update();

ALTER TABLE annotations
    ADD COLUMN target_association_id TEXT;

ALTER TABLE annotations
    ADD CONSTRAINT annotations_target_association_exclusive
    CHECK (
        (target_kind <> 'association' AND target_association_id IS NULL)
        OR
        (target_kind = 'association'
            AND target_association_id IS NOT NULL
            AND session_id IS NULL
            AND entry_session_id IS NULL
            AND entry_index IS NULL
            AND entry_end_index IS NULL
            AND annotation_id IS NULL
            AND project_hash IS NULL)
    );

ALTER TABLE annotations
    ADD CONSTRAINT annotations_target_association_id_shape
    CHECK (
        target_association_id IS NULL
        OR target_association_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
    );

ALTER TABLE annotations
    ADD CONSTRAINT annotations_target_association_owner_fk
    FOREIGN KEY (owner_id, target_association_id)
    REFERENCES transcript_associations (owner_id, association_id)
    ON DELETE CASCADE;

CREATE INDEX idx_annotations_target_association
    ON annotations (owner_id, target_association_id)
    WHERE target_association_id IS NOT NULL;
