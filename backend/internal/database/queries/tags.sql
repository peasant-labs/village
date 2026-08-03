-- name: GetOrCreateTag :one
INSERT INTO tags (name) VALUES ($1)
ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
RETURNING *;

-- name: ListTags :many
SELECT t.id, t.name, count(tt.transcript_id) as usage_count
FROM tags t
LEFT JOIN transcript_tags tt ON t.id = tt.tag_id
GROUP BY t.id, t.name
ORDER BY t.name;

-- name: ListPopularTags :many
SELECT t.id, t.name, count(tt.transcript_id) as usage_count
FROM tags t
JOIN transcript_tags tt ON t.id = tt.tag_id
GROUP BY t.id, t.name
ORDER BY usage_count DESC
LIMIT $1;

-- name: LinkTranscriptTag :exec
INSERT INTO transcript_tags (transcript_id, tag_id) VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: UnlinkTranscriptTags :exec
DELETE FROM transcript_tags WHERE transcript_id = $1;

-- name: GetTranscriptTags :many
SELECT t.id, t.name
FROM tags t
JOIN transcript_tags tt ON t.id = tt.tag_id
WHERE tt.transcript_id = $1;
