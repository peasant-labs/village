DROP INDEX IF EXISTS idx_annotations_target_transcript;
ALTER TABLE annotations DROP CONSTRAINT IF EXISTS annotations_target_transcript_id_fk;
ALTER TABLE annotations DROP COLUMN IF EXISTS target_transcript_id;
