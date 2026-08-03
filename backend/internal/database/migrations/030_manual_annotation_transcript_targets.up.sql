-- Village-created manual labels may be authored by a viewer rather than the
-- transcript owner. Their owner-local entry/session identifiers are therefore
-- not a safe locator for a public or shared transcript. Keep the wire target
-- arm unchanged, but persist the exact transcript identity for local lookup.
ALTER TABLE annotations
    ADD COLUMN IF NOT EXISTS target_transcript_id UUID;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'annotations_target_transcript_id_fk'
    ) THEN
        ALTER TABLE annotations
            ADD CONSTRAINT annotations_target_transcript_id_fk
            FOREIGN KEY (target_transcript_id)
            REFERENCES transcripts (id)
            ON DELETE CASCADE;
    END IF;
END;
$$;

CREATE INDEX IF NOT EXISTS idx_annotations_target_transcript
    ON annotations (target_transcript_id)
    WHERE target_transcript_id IS NOT NULL;

-- Backfill only Village-created human session/entry labels where the historic
-- local target resolves to exactly one transcript in the full catalog. The
-- annotator may be a viewer rather than the transcript publisher. Unresolved
-- legacy rows deliberately remain NULL and continue using their existing
-- owner-scoped local-id discovery path.
WITH uniquely_resolved AS (
    SELECT a.id, MAX(t.id::text)::uuid AS target_transcript_id
    FROM annotations a
    JOIN transcripts t
      ON (
          (a.entry_session_id IS NOT NULL AND t.local_id = a.entry_session_id)
          OR (a.session_id IS NOT NULL AND t.local_id = a.session_id)
      )
    WHERE a.target_transcript_id IS NULL
      AND a.annotator_kind = 'human'
      AND a.target_kind IN ('session', 'entry')
    GROUP BY a.id
    HAVING COUNT(DISTINCT t.id) = 1
)
UPDATE annotations a
SET target_transcript_id = resolved.target_transcript_id
FROM uniquely_resolved resolved
WHERE a.id = resolved.id;
