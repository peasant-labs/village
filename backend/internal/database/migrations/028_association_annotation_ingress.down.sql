DROP INDEX IF EXISTS idx_annotations_target_association;
ALTER TABLE annotations DROP CONSTRAINT IF EXISTS annotations_target_association_owner_fk;
ALTER TABLE annotations DROP CONSTRAINT IF EXISTS annotations_target_association_id_shape;
ALTER TABLE annotations DROP CONSTRAINT IF EXISTS annotations_target_association_exclusive;
ALTER TABLE annotations DROP COLUMN IF EXISTS target_association_id;

DROP TRIGGER IF EXISTS trg_transcript_associations_immutable ON transcript_associations;
DROP FUNCTION IF EXISTS prevent_transcript_association_update();
DROP INDEX IF EXISTS idx_transcript_associations_transcript;
DROP TABLE IF EXISTS transcript_associations;

ALTER TABLE transcripts DROP CONSTRAINT IF EXISTS transcripts_owner_id_id_key;
