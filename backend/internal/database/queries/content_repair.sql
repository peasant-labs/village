-- name: RepairTranscriptContentIdentity :execrows
-- The expected pair is the row's raw database columns. It is not the
-- established identity recorded in the ledger, which may be NULL for a
-- malformed primary hash.
UPDATE transcripts
SET content_hash = sqlc.narg(content_hash)::text,
    blob_size_bytes = sqlc.narg(blob_size_bytes)::bigint,
    segmented_identity_hash = NULL,
    segmented_identity_size = NULL,
    content_revision = @content_revision
WHERE id = @id
  AND content_revision = @expected_content_revision
  AND content_hash IS NOT DISTINCT FROM sqlc.narg(expected_hash)::text
  AND blob_size_bytes IS NOT DISTINCT FROM sqlc.narg(expected_size)::bigint
  AND segmented_identity_hash IS NOT DISTINCT FROM sqlc.narg(expected_segmented_hash)::text
  AND segmented_identity_size IS NOT DISTINCT FROM sqlc.narg(expected_segmented_size)::bigint;

-- name: DeclareTranscriptContentUnavailable :execrows
UPDATE transcripts
SET content_state = 'unavailable'
WHERE id = @id
  AND content_state = 'present'
  AND blob_key = @expected_blob_key
  AND content_revision = @expected_content_revision
  AND content_hash IS NOT DISTINCT FROM sqlc.narg(expected_hash)::text
  AND blob_size_bytes IS NOT DISTINCT FROM sqlc.narg(expected_size)::bigint
  AND segmented_identity_hash IS NOT DISTINCT FROM sqlc.narg(expected_segmented_hash)::text
  AND segmented_identity_size IS NOT DISTINCT FROM sqlc.narg(expected_segmented_size)::bigint;

-- name: MarkTranscriptContentPresent :execrows
UPDATE transcripts
SET content_state = 'present'
WHERE id = @id
  AND content_revision = @expected_content_revision;

-- name: InsertTranscriptContentRepair :exec
INSERT INTO transcript_content_repairs (
    repair_id, phase, action, transcript_id, authorization_ref, reason,
    prior_revision, prior_identity_hash, prior_identity_size, resulting_revision,
    resulting_identity_hash, resulting_identity_size, failure_class, reconciles_repair_id
) VALUES (
    @repair_id, @phase, @action, @transcript_id, @authorization_ref, @reason,
    @prior_revision, sqlc.narg(prior_identity_hash), sqlc.narg(prior_identity_size),
    @resulting_revision, sqlc.narg(resulting_identity_hash), sqlc.narg(resulting_identity_size),
    sqlc.narg(failure_class), sqlc.narg(reconciles_repair_id)
);

-- name: ListTranscriptContentRepairPhases :many
SELECT * FROM transcript_content_repairs WHERE repair_id = @repair_id ORDER BY seq;

-- name: ListUnresolvedTranscriptContentRepairs :many
SELECT a.repair_id, a.transcript_id, a.action, a.prior_revision, a.recorded_at
FROM transcript_content_repairs a
WHERE a.phase = 'attempted'
  AND a.recorded_at < now() - @settle::interval
  AND NOT EXISTS (
      SELECT 1
      FROM transcript_content_repairs t
      WHERE t.phase <> 'attempted'
        AND (t.repair_id = a.repair_id OR t.reconciles_repair_id = a.repair_id)
  )
ORDER BY a.transcript_id, a.recorded_at, a.repair_id;
