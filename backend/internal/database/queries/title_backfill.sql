-- name: ListTranscriptTitleBackfillBatch :many
SELECT id, title, title_generated, updated_at, model_provider, project_path,
       blob_key, blob_size_bytes, content_hash, wrapped_data_key,
       encryption_algorithm, key_version
FROM transcripts
WHERE id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(batch_size);

-- name: CompareAndSwapTranscriptTitles :execrows
UPDATE transcripts
SET title = sqlc.arg(title),
    title_generated = sqlc.arg(title_generated),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND title IS NOT DISTINCT FROM sqlc.arg(expected_title)
  AND title_generated IS NOT DISTINCT FROM sqlc.arg(expected_title_generated)
  AND updated_at = sqlc.arg(expected_updated_at);
