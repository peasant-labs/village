-- Pull and content-hash queries live in this dedicated source file so sqlc emits
-- them into transcripts_pull.sql.go without colliding with transcripts.sql.go.
-- This mirrors the annotations_push.sql source and generated-file layout.

-- name: SetTranscriptContentHash :exec
-- Records the server-computed hash of the served blob bytes.
-- Called at publish (after the S3 upload), at migrate-on-read rewrite, and by the
-- content-hash backfill job.
UPDATE transcripts SET content_hash = $2 WHERE id = $1;

-- name: ListTranscriptsMissingContentHash :many
-- Returns the complete immutable descriptor and nullable prior plaintext size
-- needed by a descriptor-conditional identity backfill.
SELECT id, blob_key, wrapped_data_key, encryption_algorithm, key_version, blob_size_bytes
FROM transcripts
WHERE content_hash IS NULL
ORDER BY published_at ASC, id;

-- name: ListPullableTranscripts :many
-- Returns the full rows the requester may pull, encoding the
-- canPullTranscript policy's two surviving clauses (owner OR group-shared with an
-- APPROVED share to a group the requester is a member of). Public visibility and
-- collective-owner preview are DELIBERATELY excluded (pull policy is narrower than
-- canViewTranscript). This projects every column pullTranscriptInfo needs —
-- including content_hash, the owner's github_username (users join), and a
-- per-row annotation count (correlated subquery keyed on transcript UUID, matching
-- CountTranscriptAnnotations) — in ONE query per page, so the list handler does
-- not issue separate per-row transcript, content-hash, annotation-count, or owner
-- lookups. Together with the count query, listing takes two database round trips
-- instead of scaling with page size. Offset pagination (MVP). Newest-first by
-- published_at for stable listing.
SELECT DISTINCT t.id, t.local_id, t.owner_id, u.github_username AS owner_username,
       t.title, t.model_provider, t.project_name, t.visibility, t.schema_version,
       t.published_at, t.updated_at, t.content_hash, t.license_id,
        (SELECT COUNT(*) FROM annotations a
            WHERE a.target_transcript_id = t.id
               OR (a.owner_id = t.owner_id AND (
                   a.session_id = t.local_id
                   OR a.entry_session_id = t.local_id
                 ))
              OR EXISTS (
                  SELECT 1 FROM transcript_associations ta
                  WHERE ta.owner_id = a.owner_id
                    AND ta.association_id = a.target_association_id
                    AND ta.transcript_id = t.id
              )) AS annotation_count
FROM transcripts t
JOIN users u ON u.id = t.owner_id
LEFT JOIN transcript_shares ts
    ON ts.transcript_id = t.id AND t.visibility = 'shared' AND ts.status = 'approved'
LEFT JOIN group_members gm ON gm.group_id = ts.group_id AND gm.user_id = $1
WHERE t.owner_id = $1 OR gm.user_id = $1
ORDER BY t.published_at DESC, t.id
LIMIT $2 OFFSET $3;

-- name: CountPullableTranscripts :one
-- Returns PullListResponse.Total, mirroring
-- ListPullableTranscripts's predicate exactly.
SELECT COUNT(DISTINCT t.id)
FROM transcripts t
LEFT JOIN transcript_shares ts
    ON ts.transcript_id = t.id AND t.visibility = 'shared' AND ts.status = 'approved'
LEFT JOIN group_members gm ON gm.group_id = ts.group_id AND gm.user_id = $1
WHERE t.owner_id = $1 OR gm.user_id = $1;

-- name: CountTranscriptAnnotations :one
-- Counts annotations for a single transcript's PullTranscriptInfo
-- (the single-id meta route). The list route batches this via the correlated
-- subquery in ListPullableTranscripts. Annotations target a transcript by its
-- transcript UUID. Village-created manual targets use the exact UUID; pushed
-- session and entry targets use the transcript's owner-scoped local id;
-- association targets use the owner-scoped ledger, mirroring
-- ListAnnotationsByTranscriptID's predicate.
SELECT COUNT(*) FROM annotations a
JOIN transcripts t ON t.id = @transcript_id
WHERE a.target_transcript_id = t.id
   OR (a.owner_id = t.owner_id AND (
       a.session_id = t.local_id
       OR a.entry_session_id = t.local_id
   ))
   OR EXISTS (
       SELECT 1
       FROM transcript_associations ta
       WHERE ta.owner_id = a.owner_id
         AND ta.association_id = a.target_association_id
         AND ta.transcript_id = t.id
   );

-- name: ListPullableTranscriptsByIDs :many
-- Pull skip-gate: of the client's requested transcript ids, the ones the caller
-- may PULL, reusing the ListPullableTranscripts pull predicate (owner OR an
-- approved group-share) narrowed to id = ANY($2). A requested id the caller may
-- NOT pull is simply ABSENT from the result, so the handler withholds its
-- currency by omission rather than emitting a per-id denial oracle. Projects the
-- id, the served-blob content_hash (nullable), and the local_id used to link
-- annotations. Keyed on the transcript id (PK), never on content_hash.
SELECT DISTINCT t.id, t.content_hash, t.local_id
FROM transcripts t
LEFT JOIN transcript_shares ts
    ON ts.transcript_id = t.id AND t.visibility = 'shared' AND ts.status = 'approved'
LEFT JOIN group_members gm ON gm.group_id = ts.group_id AND gm.user_id = @user_id
WHERE (t.owner_id = @user_id OR gm.user_id = @user_id) AND t.id = ANY(@transcript_ids::uuid[]);

-- name: CompareAndSwapContentIdentity :execrows
UPDATE transcripts
SET content_hash = @content_hash,
    blob_size_bytes = @plaintext_size
WHERE id = @id
  AND blob_key = @expected_blob_key
  AND wrapped_data_key = @expected_wrapped_data_key
  AND encryption_algorithm = @expected_encryption_algorithm
  AND key_version = @expected_key_version
  AND content_hash IS NULL
  AND blob_size_bytes IS NOT DISTINCT FROM @expected_prior_blob_size_bytes;

-- name: CompareAndSwapTranscriptBlob :one
UPDATE transcripts
SET blob_key = @blob_key,
    wrapped_data_key = @wrapped_data_key,
    encryption_algorithm = @encryption_algorithm,
    key_version = @key_version,
    content_hash = @content_hash,
    blob_size_bytes = @plaintext_size,
    updated_at = now()
WHERE id = @id
  AND blob_key = @expected_blob_key
  AND wrapped_data_key = @expected_wrapped_data_key
  AND encryption_algorithm = @expected_encryption_algorithm
  AND key_version = @expected_key_version
RETURNING id, owner_id, local_id, title, description, visibility, model_provider,
    model_name, harness_version, session_start, session_end, turn_count, token_count,
    blob_key, blob_size_bytes, schema_version, published_at, updated_at, parent_session_id,
    ingested_at, source_file_path, source_format, git_branch, git_remote, git_worktree,
    project_hash, project_path, project_name, tool_call_count, subagent_count, duration_ms,
    subagents, diagnostics_warnings, diagnostics_partial, tokens_in, tokens_out,
    title_generated, outcome, files_touched, lines_changed, retry_loops, retry_tokens_wasted,
    within_session_reverts, signal_density, spec_quality_score, exploration_ratio,
    scope_breadth, discovery_turns, m2_token_outcome_ratio, m3_unique_tool_count,
    m4_error_recovery_count, m4_consecutive_error_max, m5_context_utilization_pct,
    m5_peak_context_tokens, m5_avg_message_tokens, m6_output_survival_pct,
    m6_lines_survived, m6_lines_total, m7_spec_word_count, m7_spec_has_examples,
    m7_spec_has_constraints, computed_at, compute_version, content_hash, license_id,
    wrapped_data_key, encryption_algorithm, key_version, accepted_request_operation_fingerprint;

-- name: ListTranscriptDescriptorsForRewrap :many
SELECT id, blob_key, wrapped_data_key, encryption_algorithm, key_version
FROM transcripts
WHERE key_version < @active_key_version
  AND (key_version > @after_key_version
       OR (key_version = @after_key_version AND id > @after_id))
ORDER BY key_version, id
LIMIT @batch_size;

-- name: CompareAndSwapWrappedDataKey :execrows
UPDATE transcripts
SET wrapped_data_key = @wrapped_data_key,
    key_version = @key_version,
    updated_at = now()
WHERE id = @id
  AND blob_key = @expected_blob_key
  AND wrapped_data_key = @expected_wrapped_data_key
  AND encryption_algorithm = @expected_encryption_algorithm
  AND key_version = @expected_key_version;

-- name: DeleteTranscriptReturningDescriptor :one
DELETE FROM transcripts
WHERE id = @id
RETURNING blob_key, wrapped_data_key, encryption_algorithm, key_version;
