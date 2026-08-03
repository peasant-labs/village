-- name: InsertTranscriptCommits :exec
-- Batched multi-row upsert of a transcript's commits in a SINGLE statement
-- passes commits as one JSONB array expanded via jsonb_to_recordset, so the
-- whole payload is one round trip instead of N
-- per-row INSERTs. JSONB preserves SQL NULLs (a missing/null field maps to a
-- NULL column), unlike parallel text[]/int[] arrays. Idempotent on
-- (transcript_id, sha): a repeated SHA refreshes the row in place.
INSERT INTO transcript_commits (
    transcript_id, commit_order, sha, message,
    author_name, author_email, additions, deletions,
    authored_at, committed_at
)
SELECT
    @transcript_id::uuid,
    r.commit_order, r.sha, r.message,
    r.author_name, r.author_email, r.additions, r.deletions,
    r.authored_at, r.committed_at
FROM jsonb_to_recordset(@commits::jsonb) AS r(
    commit_order int,
    sha text,
    message text,
    author_name text,
    author_email text,
    additions int,
    deletions int,
    authored_at timestamptz,
    committed_at timestamptz
)
ON CONFLICT (transcript_id, sha) DO UPDATE SET
    commit_order = EXCLUDED.commit_order,
    message      = EXCLUDED.message,
    author_name  = EXCLUDED.author_name,
    author_email = EXCLUDED.author_email,
    additions    = EXCLUDED.additions,
    deletions    = EXCLUDED.deletions,
    authored_at  = EXCLUDED.authored_at,
    committed_at = EXCLUDED.committed_at;

-- name: DeleteTranscriptCommits :exec
-- Removes all commit rows for a transcript. Used on re-publish to drop commits
-- that are no longer present in the latest payload before re-inserting.
DELETE FROM transcript_commits WHERE transcript_id = $1;

-- name: ListTranscriptCommits :many
-- Returns every commit linked to a transcript, in payload order.
SELECT * FROM transcript_commits
WHERE transcript_id = $1
ORDER BY commit_order ASC, id ASC;
