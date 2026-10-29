-- name: InsertIntegrityResult :exec
-- One answer per (transcript, content, checker version). A repeat of the same
-- pair keeps the first answer.
INSERT INTO transcript_integrity_results (
    transcript_id, content_hash, scry_version, verdict, flagged_turns
) VALUES (
    @transcript_id, @content_hash, @scry_version, @verdict, @flagged_turns
)
ON CONFLICT DO NOTHING;

-- name: HasOpenIntegrityFlag :one
SELECT EXISTS (
    SELECT 1 FROM transcript_integrity_open_flags WHERE transcript_id = @transcript_id
)::bool AS open;

-- name: ServedIntegrityForContent :one
-- What the detail route serves, in one read: whether the transcript has an
-- open flag, and the positions of the latest checked answer for the one
-- content the response names. Positions of any other content are never
-- returned: they are empty when the flag is open but nothing applies to this
-- content. Versions are compared as numbers, never as text.
SELECT
    EXISTS (
        SELECT 1 FROM transcript_integrity_open_flags f WHERE f.transcript_id = @transcript_id
    )::bool AS open,
    COALESCE((
        SELECT r.flagged_turns
        FROM transcript_integrity_results r
        WHERE r.transcript_id = @transcript_id
          AND r.content_hash = @content_hash
          AND r.verdict = 'checked'
        ORDER BY string_to_array(r.scry_version, '.')::numeric[] DESC
        LIMIT 1
    ), '{}'::int[])::int[] AS flagged_turns;

-- name: RecordIntegrityTombstone :exec
-- Run in the delete's transaction, before the row goes. Writes nothing when
-- no flag is open. A second record for the same publisher session keeps the
-- earlier flag date.
INSERT INTO integrity_flag_tombstones (owner_id, local_id, flagged_at)
SELECT t.owner_id, t.local_id, min(f.flagged_at)
FROM transcripts t
JOIN transcript_integrity_open_flags f ON f.transcript_id = t.id
WHERE t.id = @transcript_id
GROUP BY t.owner_id, t.local_id
ON CONFLICT (owner_id, local_id) DO UPDATE
SET flagged_at = LEAST(integrity_flag_tombstones.flagged_at, EXCLUDED.flagged_at);

-- name: InheritIntegrityTombstone :exec
-- Run in a first publish's transaction: a publisher session whose flagged copy
-- was deleted starts out flagged, dated by the record.
INSERT INTO transcript_integrity_inherited_flags (transcript_id, flagged_at)
SELECT @transcript_id, flagged_at
FROM integrity_flag_tombstones
WHERE owner_id = @owner_id AND local_id = @local_id
ON CONFLICT DO NOTHING;

-- name: ListIntegrityCandidates :many
-- The transcripts the background check owes an answer under one checker
-- version: in the checked set (public, or shared to any collective in any
-- share status), with a known content hash, no result for that content under
-- this version, and no attempt for it that is finished, waiting out its
-- backoff, or already at the attempt cap. Content with no result under any version comes first, so new
-- content is checked before a version sweep; then the most recently updated.
SELECT t.*
FROM transcripts t
WHERE (t.visibility = 'public' OR EXISTS (SELECT 1 FROM transcript_shares s WHERE s.transcript_id = t.id))
  AND t.content_hash IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM transcript_integrity_results r
      WHERE r.transcript_id = t.id AND r.content_hash = t.content_hash AND r.scry_version = @scry_version
  )
  AND NOT EXISTS (
      SELECT 1 FROM transcript_integrity_attempts a
      WHERE a.transcript_id = t.id AND a.content_hash = t.content_hash AND a.scry_version = @scry_version
        AND (a.status IN ('done', 'skipped', 'failed') OR a.next_attempt_at > now() OR a.attempts >= @max_attempts::int)
  )
ORDER BY EXISTS (
      SELECT 1 FROM transcript_integrity_results r
      WHERE r.transcript_id = t.id AND r.content_hash = t.content_hash
  ) ASC, t.updated_at DESC
LIMIT @batch::int;

-- name: IsInIntegrityCheckedSet :one
-- Whether anyone but the owner can read the transcript now. The predicate is
-- the same text as ListIntegrityCandidates'.
SELECT EXISTS (
    SELECT 1 FROM transcripts t
    WHERE t.id = @id
      AND (t.visibility = 'public' OR EXISTS (SELECT 1 FROM transcript_shares s WHERE s.transcript_id = t.id))
)::bool AS in_set;

-- name: ClaimIntegrityAttempt :one
-- Takes one check attempt for (transcript, content, version) and holds it
-- for hold_seconds. It returns the attempt count after taking it, or no row
-- when the attempt is finished, at the cap, or another holder's time has not
-- passed; the
-- conditional upsert locks the row, so two instances cannot both take it.
-- Every time is the database's own now(), so an application clock can never
-- shorten a hold.
INSERT INTO transcript_integrity_attempts (
    transcript_id, content_hash, scry_version, status, attempts, next_attempt_at
) VALUES (
    @transcript_id, @content_hash, @scry_version, 'pending', 1,
    now() + make_interval(secs => @hold_seconds::float8)
)
ON CONFLICT (transcript_id, content_hash, scry_version) DO UPDATE
SET attempts = transcript_integrity_attempts.attempts + 1,
    status = 'pending',
    next_attempt_at = EXCLUDED.next_attempt_at,
    updated_at = now()
WHERE transcript_integrity_attempts.status = 'pending'
  AND transcript_integrity_attempts.next_attempt_at <= now()
  AND transcript_integrity_attempts.attempts < @max_attempts::int
RETURNING attempts;

-- name: CompleteIntegrityAttempt :exec
-- Records how a taken attempt ended. last_error is a closed set, never raw
-- error text. The attempt is due again retry_after_seconds after the
-- database's now().
UPDATE transcript_integrity_attempts
SET status = @status,
    last_error = sqlc.narg(last_error),
    next_attempt_at = now() + make_interval(secs => @retry_after_seconds::float8),
    updated_at = now()
WHERE transcript_id = @transcript_id
  AND content_hash = @content_hash
  AND scry_version = @scry_version;

-- name: ReleaseIntegrityAttempt :exec
-- Gives back a taken attempt that sent nothing because the transcript was no
-- longer a candidate when it was read: a first attempt leaves no row, a later
-- one is due again with its earlier count.
WITH released AS (
    UPDATE transcript_integrity_attempts u
    SET attempts = u.attempts - 1, next_attempt_at = now(), updated_at = now()
    WHERE u.transcript_id = @transcript_id
      AND u.content_hash = @content_hash
      AND u.scry_version = @scry_version
      AND u.status = 'pending'
      AND u.attempts > 1
    RETURNING 1
)
DELETE FROM transcript_integrity_attempts d
WHERE d.transcript_id = @transcript_id
  AND d.content_hash = @content_hash
  AND d.scry_version = @scry_version
  AND d.status = 'pending'
  AND d.attempts = 1;
