-- name: BulkUpsertAnnotations :many
-- Batched multi-row upsert of pushed annotations in a SINGLE statement
-- replaces the per-item UpsertAnnotation loop's N round trips with one. Rows are
-- passed as one JSONB array (jsonb_to_recordset, NULL-preserving)
-- and all share the scalar owner_id, so the upsert is owner-scoped. ON CONFLICT
-- (owner_id, content_hash) bumps updated_at (the existing skip semantics). RETURNING the
-- content_hash plus (created_at = updated_at) lets the caller classify each row
-- as created (true) vs updated (false) per item — the client manifest skip-gate
-- and retraction depend on per-item outcomes.
INSERT INTO annotations (
    content_hash, owner_id, target_kind, session_id,
    entry_session_id, entry_index, entry_end_index,
    annotation_id, project_hash, target_association_id, type_id, value,
    is_primary, confidence, reason, annotator_name, provenance
)
SELECT
    r.content_hash, @owner_id::uuid, r.target_kind, r.session_id,
    r.entry_session_id, r.entry_index, r.entry_end_index,
    r.annotation_id, r.project_hash, r.target_association_id, r.type_id, r.value,
    r.is_primary, r.confidence, r.reason, r.annotator_name, r.provenance
FROM jsonb_to_recordset(@items::jsonb) AS r(
    content_hash     text,
    target_kind      text,
    session_id       text,
    entry_session_id text,
    entry_index      integer,
    entry_end_index  integer,
    annotation_id    text,
    project_hash     text,
    target_association_id text,
    type_id          text,
    value            text,
    is_primary       boolean,
    confidence       double precision,
    reason           text,
    annotator_name   text,
    provenance       jsonb
)
ON CONFLICT (owner_id, content_hash) DO UPDATE SET updated_at = now()
RETURNING content_hash, COALESCE(created_at = updated_at, false)::boolean AS created;

-- name: CreateManualAnnotation :one
-- Inserts a single entry-level annotation created manually on the village
-- (village-only, no propagation). annotator_kind is fixed to 'human'.
-- content_hash dedups identical labels: a repeat insert bumps updated_at and
-- returns the existing row rather than creating a duplicate.
INSERT INTO annotations (
    content_hash, owner_id, target_kind, session_id,
    entry_session_id, entry_index, entry_end_index,
    target_transcript_id, type_id, value, is_primary, reason, annotator_name, annotator_kind
) VALUES (
    $1, $2, 'entry', NULL,
    $3, $4, $5,
    $6, $7, $8, $9, $10, $11, 'human'
)
ON CONFLICT (owner_id, content_hash) DO UPDATE SET updated_at = now()
RETURNING *;

-- name: ListAnnotationContentHashesByOwner :many
-- Returns every annotation content-hash the village currently holds for the
-- given owner's server manifest. Index-bounded on
-- idx_annotations_owner. Hashes only — no content — so the result is
-- privacy-safe. Ordered for a deterministic wire payload.
SELECT content_hash FROM annotations
WHERE owner_id = $1
ORDER BY content_hash ASC;

-- name: ListOwnerAnnotationHashesForTranscriptIDs :many
-- Pull skip-gate: the requester's OWN annotation content-hashes that link to any
-- of the given transcript UUIDs, for the owner-scoped annotationsCurrent compare.
-- The UUID is the identity boundary: owner-local session ids are not globally
-- unique. Village-created manual rows use target_transcript_id directly; pushed
-- session and entry arms additionally require t.owner_id = a.owner_id;
-- association arms require the ledger's exact transcript UUID.
-- Hashes only, so the result stays privacy-safe.
SELECT a.content_hash,
       t.id AS transcript_id
FROM annotations a
JOIN transcripts t ON t.id = ANY(@transcript_ids::uuid[])
WHERE a.owner_id = @owner_id
   AND (
       a.target_transcript_id = t.id
       OR
       (a.owner_id = t.owner_id AND (
          a.session_id = t.local_id
          OR a.entry_session_id = t.local_id
      ))
      OR EXISTS (
          SELECT 1
          FROM transcript_associations ta
          WHERE ta.owner_id = a.owner_id
            AND ta.association_id = a.target_association_id
            AND ta.transcript_id = t.id
      )
  );

-- name: DeleteAnnotationByContentHash :exec
-- Hard-deletes the owner-scoped annotation identified by content_hash
-- during retraction. Owner-scoped so a caller can never drop another owner's
-- copy even on a hash collision across owners. Idempotent: deleting an
-- absent (already-gone) hash affects zero rows and is not an error.
DELETE FROM annotations
WHERE content_hash = $1 AND owner_id = $2;

-- name: ListAnnotationsByTranscriptID :many
-- Returns every annotation whose target resolves to the exact transcript UUID,
-- ordered oldest first. Village-created manual rows use target_transcript_id;
-- pushed local session ids are owner-scoped, so their direct session and entry
-- arms join through both the transcript UUID and owner. Association arms use the
-- immutable ledger's exact transcript_id.
SELECT a.* FROM annotations a
JOIN transcripts t ON t.id = $1
WHERE (a.owner_id = t.owner_id AND (
	       a.session_id = t.local_id
	       OR a.entry_session_id = t.local_id
	   ))
	   OR a.target_transcript_id = t.id
	   OR EXISTS (
       SELECT 1
       FROM transcript_associations ta
       WHERE ta.owner_id = a.owner_id
         AND ta.association_id = a.target_association_id
         AND ta.transcript_id = t.id
   )
ORDER BY a.created_at ASC, a.id ASC;
