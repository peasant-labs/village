-- Integrity state for published transcripts. Nothing here holds content, rule
-- names or provider text, and none of these tables is a disclosure axis, so
-- none needs the governance audit actor.

-- One row per (content, checker version): a history and a cache, never
-- deleted on republish. flagged_turns are zero-based positions in the top
-- level turns of the content the hash names. Whether a transcript is flagged
-- is derived (verdict 'checked' with turns), never stored.
CREATE TABLE transcript_integrity_results (
    transcript_id UUID        NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    content_hash  TEXT        NOT NULL,
    scry_version  TEXT        NOT NULL,
    verdict       TEXT        NOT NULL,
    flagged_turns INTEGER[]   NOT NULL DEFAULT '{}',
    checked_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (transcript_id, content_hash, scry_version),
    CONSTRAINT transcript_integrity_results_hash_shape CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT transcript_integrity_results_version_shape CHECK (scry_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    CONSTRAINT transcript_integrity_results_verdict_closed CHECK (verdict IN ('checked', 'unchecked')),
    CONSTRAINT transcript_integrity_results_unchecked_has_no_turns CHECK (verdict = 'checked' OR cardinality(flagged_turns) = 0)
);

-- A flag with no result behind it: a later publish of a session whose
-- flagged copy was deleted. flagged_at is copied from the record it was
-- inherited from.
CREATE TABLE transcript_integrity_inherited_flags (
    transcript_id UUID        PRIMARY KEY REFERENCES transcripts(id) ON DELETE CASCADE,
    flagged_at    TIMESTAMPTZ NOT NULL
);

-- What deleting a flagged transcript leaves behind: the owner and the
-- publisher's own session id, and when it was flagged. No content, no hash,
-- no turns. Keyed on the owner and local id because the transcript row is
-- gone; it is removed with the account.
CREATE TABLE integrity_flag_tombstones (
    owner_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    local_id    TEXT        NOT NULL,
    flagged_at  TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, local_id),
    CONSTRAINT integrity_flag_tombstones_local_id_nonempty CHECK (length(local_id) > 0)
);

-- The open flags, one row each: every inherited flag, and every flagged
-- result that no later version's checked result with no turns on the same
-- content voids. A transcript is flagged when it has a row here. Versions are
-- compared as numbers ('1.10.0' is later than '1.9.0'), never as text.
-- Inherited flags and flags on replaced content are never void.
CREATE VIEW transcript_integrity_open_flags AS
SELECT i.transcript_id, i.flagged_at
FROM transcript_integrity_inherited_flags i
UNION ALL
SELECT r.transcript_id, r.checked_at AS flagged_at
FROM transcript_integrity_results r
WHERE r.verdict = 'checked'
  AND cardinality(r.flagged_turns) > 0
  AND NOT EXISTS (
      SELECT 1
      FROM transcript_integrity_results v
      WHERE v.transcript_id = r.transcript_id
        AND v.content_hash = r.content_hash
        AND v.verdict = 'checked'
        AND cardinality(v.flagged_turns) = 0
        AND string_to_array(v.scry_version, '.')::numeric[] > string_to_array(r.scry_version, '.')::numeric[]
  );

-- The ledger of check attempts. last_error is a closed set so raw error text,
-- and with it content, cannot be stored.
CREATE TABLE transcript_integrity_attempts (
    transcript_id   UUID        NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    content_hash    TEXT        NOT NULL,
    scry_version    TEXT        NOT NULL,
    status          TEXT        NOT NULL,
    attempts        INTEGER     NOT NULL DEFAULT 0,
    last_error      TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (transcript_id, content_hash, scry_version),
    CONSTRAINT transcript_integrity_attempts_hash_shape CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT transcript_integrity_attempts_version_shape CHECK (scry_version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
    CONSTRAINT transcript_integrity_attempts_status_closed CHECK (status IN ('pending', 'done', 'skipped', 'failed')),
    CONSTRAINT transcript_integrity_attempts_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT transcript_integrity_attempts_last_error_closed CHECK (
        last_error IS NULL OR last_error IN ('timeout', 'unavailable', 'rejected', 'invalid_response', 'too_large', 'read_failed', 'legacy_content')
    )
);

-- The one scan no primary key serves: the next attempts that are due.
CREATE INDEX transcript_integrity_attempts_due_idx
    ON transcript_integrity_attempts (next_attempt_at)
    WHERE status = 'pending';
