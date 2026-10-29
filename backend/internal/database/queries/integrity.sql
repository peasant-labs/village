-- name: InsertIntegrityResult :exec
-- One answer per (transcript, content, checker version). A repeat of the same
-- pair keeps the first answer.
INSERT INTO transcript_integrity_results (
    transcript_id, content_hash, scry_version, verdict, flagged_turns
) VALUES (
    @transcript_id, @content_hash, @scry_version, @verdict, @flagged_turns
)
ON CONFLICT DO NOTHING;

-- name: LatestIntegrityResultForContent :one
-- The latest checked answer for one content. An unchecked answer examined
-- nothing and carries no positions, so it never hides the positions of an
-- earlier checked answer. Versions are compared as numbers, never as text.
SELECT transcript_id, content_hash, scry_version, verdict, flagged_turns, checked_at
FROM transcript_integrity_results
WHERE transcript_id = @transcript_id
  AND content_hash = @content_hash
  AND verdict = 'checked'
ORDER BY string_to_array(scry_version, '.')::numeric[] DESC
LIMIT 1;

-- name: HasOpenIntegrityFlag :one
SELECT EXISTS (
    SELECT 1 FROM transcript_integrity_open_flags WHERE transcript_id = @transcript_id
)::bool AS open;

-- name: RecordIntegrityTombstone :exec
-- Run in the delete's transaction, before the row goes. Writes nothing when
-- no flag is open. A second record for the same publisher session keeps the
-- earlier flag date.
INSERT INTO integrity_flag_tombstones (owner_id, local_id, flagged_at)
SELECT t.owner_id, t.local_id, min(f.flagged_at)
FROM transcripts t
JOIN transcript_integrity_open_flags f ON f.transcript_id = t.id
WHERE t.id = @transcript_id
GROUP BY t.owner_id, t.local_id
ON CONFLICT (owner_id, local_id) DO UPDATE
SET flagged_at = LEAST(integrity_flag_tombstones.flagged_at, EXCLUDED.flagged_at);

-- name: InheritIntegrityTombstone :exec
-- Run in a first publish's transaction: a publisher session whose flagged copy
-- was deleted starts out flagged, dated by the record.
INSERT INTO transcript_integrity_inherited_flags (transcript_id, flagged_at)
SELECT @transcript_id, flagged_at
FROM integrity_flag_tombstones
WHERE owner_id = @owner_id AND local_id = @local_id
ON CONFLICT DO NOTHING;
