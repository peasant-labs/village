-- name: CreateAttestation :one
INSERT INTO attestations (transcript_id, attester_id, org_login, attestation_type, note)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: DeleteAttestation :exec
DELETE FROM attestations
WHERE id = $1 AND attester_id = $2;

-- name: ListTranscriptAttestations :many
SELECT a.id, a.transcript_id, a.org_login, a.attestation_type, a.note, a.created_at,
       u.github_username as attester_username, u.avatar_url as attester_avatar
FROM attestations a
JOIN users u ON a.attester_id = u.id
WHERE a.transcript_id = $1
ORDER BY a.created_at DESC;

-- name: ListAttestationsByTranscriptIDs :many
SELECT a.transcript_id, a.org_login, a.attestation_type, a.created_at
FROM attestations a
WHERE a.transcript_id = ANY($1::uuid[])
ORDER BY a.created_at DESC;
