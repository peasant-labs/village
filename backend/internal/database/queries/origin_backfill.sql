-- name: ListTranscriptOriginBackfillBatch :many
SELECT id, session_origin, updated_at,
       blob_key, blob_size_bytes, content_hash, wrapped_data_key,
       encryption_algorithm, key_version
FROM transcripts
WHERE id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(batch_size);

-- name: CompareAndSwapTranscriptSessionOrigin :execrows
UPDATE transcripts
SET session_origin = sqlc.arg(session_origin),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND session_origin = sqlc.arg(expected_session_origin)
  AND updated_at = sqlc.arg(expected_updated_at);
