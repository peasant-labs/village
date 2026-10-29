-- name: SetTranscriptPublishMetadataObject :exec
UPDATE transcripts
SET publish_metadata_key = sqlc.narg(publish_metadata_key),
    publish_metadata_hash = sqlc.narg(publish_metadata_hash),
    publish_metadata_size = sqlc.narg(publish_metadata_size)
WHERE id = @id;

-- name: ReplaceTranscriptPublishMetadataObject :execrows
UPDATE transcripts
SET publish_metadata_key = sqlc.narg(publish_metadata_key),
    publish_metadata_hash = sqlc.narg(publish_metadata_hash),
    publish_metadata_size = sqlc.narg(publish_metadata_size)
WHERE id = @id
  AND publish_metadata_key IS NOT DISTINCT FROM sqlc.narg(expected_publish_metadata_key)::text;

-- name: RecordTranscriptContentRewrite :exec
INSERT INTO transcript_content_rewrites (
    transcript_id, replaced_content_hash, installed_content_hash, replaced_was_legacy
) VALUES (
    @transcript_id, @replaced_content_hash, @installed_content_hash, @replaced_was_legacy
)
ON CONFLICT DO NOTHING;

-- name: IsLegacyLineageContent :one
-- True when the content with this hash was installed by a chain of Village
-- rewrites that began from legacy JSONL. The walk goes from the installed
-- hash back through each replaced hash; any legacy step makes the chain legacy.
-- UNION (not UNION ALL) and no depth column, so a cycle stops once every row
-- has been seen.
WITH RECURSIVE lineage(installed_content_hash, replaced_content_hash, replaced_was_legacy) AS (
    SELECT r.installed_content_hash, r.replaced_content_hash, r.replaced_was_legacy
    FROM transcript_content_rewrites r
    WHERE r.transcript_id = @transcript_id
      AND r.installed_content_hash = @content_hash
    UNION
    SELECT r.installed_content_hash, r.replaced_content_hash, r.replaced_was_legacy
    FROM transcript_content_rewrites r
    JOIN lineage l ON r.installed_content_hash = l.replaced_content_hash
    WHERE r.transcript_id = @transcript_id
)
SELECT EXISTS (SELECT 1 FROM lineage WHERE replaced_was_legacy)::bool AS is_legacy;
