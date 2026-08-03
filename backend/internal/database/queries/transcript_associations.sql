-- name: ListTranscriptAssociationsByOwnerAndIDs :many
SELECT * FROM transcript_associations
WHERE owner_id = @owner_id
  AND association_id = ANY(@association_ids::text[]);

-- name: ListTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashes :many
SELECT * FROM transcript_associations
WHERE owner_id = @owner_id
  AND transcript_id = @transcript_id
  AND observed_commit_sha = ANY(@observed_commit_hashes::text[]);

-- name: InsertTranscriptAssociations :exec
INSERT INTO transcript_associations (
    owner_id, association_id, transcript_id, observed_commit_sha
)
SELECT
    @owner_id::uuid,
    item.association_id,
    @transcript_id::uuid,
    item.observed_commit_sha
FROM jsonb_to_recordset(@items::jsonb) AS item(
    association_id text,
    observed_commit_sha text
);

-- name: ListTranscriptAssociationIDsByOwner :many
SELECT association_id
FROM transcript_associations
WHERE owner_id = @owner_id
  AND association_id = ANY(@association_ids::text[]);

-- name: ListTranscriptAssociationsByTranscript :many
SELECT *
FROM transcript_associations
WHERE transcript_id = $1
ORDER BY association_id, observed_commit_sha;
