-- name: CreateAttestation :one
INSERT INTO attestations (transcript_id, attester_id, org_login, attestation_type, note)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: DeleteAttestation :exec
DELETE FROM attestations
WHERE id = $1 AND attester_id = $2;

-- name: ListTranscriptAttestations :many
-- The attester is attester_id only: the handler shows them as the viewer may
-- see them (ListPublicOwnersForViewer), never from columns read here.
SELECT a.id, a.transcript_id, a.org_login, a.attestation_type, a.note, a.created_at,
       a.attester_id
FROM attestations a
WHERE a.transcript_id = $1
ORDER BY a.created_at DESC;

-- name: ListAttestationsByTranscriptIDs :many
-- attester_id is read so the handler can withhold the organization of an
-- attester who is anonymous to the viewer; it is never served.
SELECT a.transcript_id, a.org_login, a.attestation_type, a.created_at, a.attester_id
FROM attestations a
WHERE a.transcript_id = ANY($1::uuid[])
ORDER BY a.created_at DESC;
