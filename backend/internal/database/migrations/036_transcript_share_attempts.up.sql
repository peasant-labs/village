-- Every submission of a transcript to a collective is its own attempt.
--
-- Before this migration transcript_shares held ONE row per (transcript, group)
-- and the share insert used ON CONFLICT DO NOTHING, so re-sharing after a
-- rejection was a silent no-op and the rejection history was unrecoverable.
-- An attempt is now an instance: a rejection closes attempt N, and a new
-- submission opens attempt N+1.
--
-- transcript_shares survives as the CURRENT-STATE row, derived from the latest
-- attempt by trg_derive_transcript_share below. It is kept as a physical table
-- rather than replaced by a view because idx_transcript_shares_group and
-- idx_transcript_shares_status carry hot joins that a "latest attempt per pair"
-- view would turn into a window-function scan.
CREATE TABLE transcript_share_attempts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transcript_id UUID NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    group_id      UUID NOT NULL REFERENCES groups(id)      ON DELETE CASCADE,
    event_num     INT  NOT NULL CHECK (event_num >= 1),
    status        VARCHAR(20) NOT NULL
                   CHECK (status IN ('pending', 'approved', 'rejected',
                                     'retracted', 'revoked')),
    recorded_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at    TIMESTAMPTZ,
    decided_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    UNIQUE (transcript_id, group_id, event_num)
);

-- At most ONE open attempt per (transcript, group): a second submission while
-- one is already pending is a duplicate, not a new instance.
--
-- Every terminal transition MUST close the open attempt. If a withdrawal only
-- removed the current-state row and left the attempt pending, this index would
-- block re-submission forever with no cause the user can see.
CREATE UNIQUE INDEX uq_share_attempt_open
    ON transcript_share_attempts (transcript_id, group_id)
    WHERE status = 'pending';

CREATE INDEX idx_share_attempts_group_status
    ON transcript_share_attempts (group_id, status);

-- Existing shares become attempt 1 carrying their current status, so counters
-- are correct for pre-existing data from the first deploy. This runs before the
-- triggers exist: the current-state rows it derives from are already present
-- and already correct, so there is nothing for the derivation to do.
INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status, recorded_at)
SELECT transcript_id, group_id, 1, status, COALESCE(shared_at, now())
FROM transcript_shares;

-- ---------------------------------------------------------------------------
-- The derivation: attempts are written, transcript_shares is derived.
--
-- transcript_shares.status keeps its shipped three-value CHECK
-- (005_github_org_affiliations: pending | approved | rejected), which this
-- migration does NOT alter. 'retracted' and 'revoked' are therefore not
-- representable there and must never be written: the current-state row is
-- DELETED instead. That is exactly what the withdrawal and removal paths did
-- before this migration, so every shipped read keeps its behaviour - a
-- withdrawn or removed contribution simply is not a share any more, while the
-- attempts table retains why.
--
-- AFTER INSERT OR UPDATE, not AFTER INSERT: moderation decisions, retractions
-- and revocations are all UPDATEs of an existing attempt. An INSERT-only
-- trigger would silently stop propagating every one of them.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION derive_transcript_share() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE
    latest_status    TEXT;
    latest_recorded TIMESTAMPTZ;
BEGIN
    SELECT a.status, a.recorded_at
      INTO latest_status, latest_recorded
      FROM transcript_share_attempts a
     WHERE a.transcript_id = NEW.transcript_id
       AND a.group_id      = NEW.group_id
     ORDER BY a.event_num DESC
     LIMIT 1;

    -- Transaction-scoped permission for the one sanctioned writer of
    -- transcript_shares. Cleared again below so the permission never outlives
    -- the derivation it was opened for.
    PERFORM set_config('app.share_state_derivation', 'on', true);

    IF latest_status IN ('pending', 'approved', 'rejected') THEN
        INSERT INTO transcript_shares (transcript_id, group_id, status, shared_at)
        VALUES (NEW.transcript_id, NEW.group_id, latest_status, latest_recorded)
        ON CONFLICT (transcript_id, group_id)
        DO UPDATE SET status = EXCLUDED.status, shared_at = EXCLUDED.shared_at;
    ELSE
        DELETE FROM transcript_shares
         WHERE transcript_id = NEW.transcript_id
           AND group_id      = NEW.group_id;
    END IF;

    PERFORM set_config('app.share_state_derivation', 'off', true);
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS trg_derive_transcript_share ON transcript_share_attempts;
CREATE TRIGGER trg_derive_transcript_share
    AFTER INSERT OR UPDATE ON transcript_share_attempts
    FOR EACH ROW EXECUTE FUNCTION derive_transcript_share();

-- ---------------------------------------------------------------------------
-- Fail-closed: application SQL cannot write transcript_shares at all.
--
-- Two successive hand-enumerations of the writer list each missed a live
-- writer, so the list is not the enforcement - this trigger is. Only the
-- derivation above may write the table, and it proves that by holding the
-- transaction-scoped app.share_state_derivation flag.
--
-- The one non-derivation write that must still succeed is a foreign-key
-- cascade: deleting a transcript, a group or an account removes the share rows
-- with no application code in the loop. A cascaded DELETE is recognised by its
-- parent already being gone, which no direct application DELETE can imitate.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION guard_transcript_shares_writer() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF current_setting('app.share_state_derivation', true) = 'on' THEN
        RETURN COALESCE(NEW, OLD);
    END IF;

    IF TG_OP = 'DELETE'
       AND (NOT EXISTS (SELECT 1 FROM transcripts WHERE id = OLD.transcript_id)
         OR NOT EXISTS (SELECT 1 FROM groups      WHERE id = OLD.group_id)) THEN
        RETURN OLD;
    END IF;

    RAISE EXCEPTION 'transcript_shares is derived, not written: % blocked. It is maintained only by '
        'trg_derive_transcript_share from transcript_share_attempts, so a direct write would be '
        'overwritten by the next attempt and would lose the attempt history. Write the attempt '
        'instead - submit, decide, retract or revoke a row of transcript_share_attempts - and the '
        'current-state row follows (docs/database-invariants.md, the share-attempt model).', TG_OP;
END $$;

DROP TRIGGER IF EXISTS trg_transcript_shares_fail_closed ON transcript_shares;
CREATE TRIGGER trg_transcript_shares_fail_closed
    BEFORE INSERT OR UPDATE OR DELETE ON transcript_shares
    FOR EACH ROW EXECUTE FUNCTION guard_transcript_shares_writer();

-- ---------------------------------------------------------------------------
-- Attempt history is immutable once decided. A new decision is a new attempt.
--
-- This is a THIRD guard, on a DIFFERENT table: the fail-closed guard above
-- protects the derived current-state row, this one protects the history.
--
-- The single permitted change to a terminal row is the decided_by FK's
-- ON DELETE SET NULL: deleting a moderator's account anonymises their past
-- decisions rather than blocking the deletion. Every other column must be
-- identical for that exemption to apply.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION guard_share_attempt_immutable() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF OLD.status NOT IN ('approved', 'rejected', 'retracted', 'revoked') THEN
        RETURN NEW;
    END IF;

    IF OLD.decided_by IS NOT NULL AND NEW.decided_by IS NULL
       AND NEW.id            =            OLD.id
       AND NEW.transcript_id =            OLD.transcript_id
       AND NEW.group_id      =            OLD.group_id
       AND NEW.event_num     =            OLD.event_num
       AND NEW.status        =            OLD.status
       AND NEW.recorded_at   =            OLD.recorded_at
       AND NEW.decided_at    IS NOT DISTINCT FROM OLD.decided_at THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'transcript_share_attempts row % is already % and cannot be modified. Attempt '
        'history is the record of what was submitted and what was decided, so a changed decision is '
        'a NEW attempt, not an edited one. Insert the next attempt for this transcript and '
        'collective instead (docs/database-invariants.md, the share-attempt model).',
        OLD.id, OLD.status;
END $$;

DROP TRIGGER IF EXISTS trg_share_attempt_immutable ON transcript_share_attempts;
CREATE TRIGGER trg_share_attempt_immutable
    BEFORE UPDATE ON transcript_share_attempts
    FOR EACH ROW EXECUTE FUNCTION guard_share_attempt_immutable();

-- ---------------------------------------------------------------------------
-- Reconstruction and verification.
--
-- The current-state row is a projection of the ledger, so it must always be
-- reconstructible FROM the ledger. That guarantee is what makes a derived table
-- acceptable rather than a second source of truth, and it is only real if
-- something exercises it - so the rebuild below is executed by the test suite,
-- not merely written down.
--
-- ONE definition of "latest", shared by the rebuild and the drift check, so the
-- two can never disagree about what they are comparing.
--
-- These views are helpers for reconstruction and verification. They are NOT a
-- replacement for transcript_shares: the projection stays a physical table
-- because its read cost is invariant to how much history accumulates behind it,
-- while a view's cost grows with every recorded event (measured, not assumed -
-- see docs/database-invariants.md).
-- ---------------------------------------------------------------------------
CREATE OR REPLACE VIEW transcript_share_latest_event AS
SELECT DISTINCT ON (transcript_id, group_id)
       transcript_id, group_id, event_num, status, recorded_at
FROM transcript_share_attempts
ORDER BY transcript_id, group_id, event_num DESC;

-- The projection rule: the three representable states become a current-state
-- row carrying the latest event's recorded_at as shared_at; the two terminal
-- states become NO ROW.
CREATE OR REPLACE VIEW transcript_share_expected_state AS
SELECT transcript_id, group_id, status, recorded_at AS shared_at
FROM transcript_share_latest_event
WHERE status IN ('pending', 'approved', 'rejected');

-- Every way the projection can disagree with the ledger. Zero rows means the
-- stored projection is exactly a latest-event fold over the whole ledger.
CREATE OR REPLACE VIEW transcript_share_drift AS
SELECT COALESCE(stored.transcript_id, expected.transcript_id) AS transcript_id,
       COALESCE(stored.group_id, expected.group_id)           AS group_id,
       CASE
           WHEN stored.transcript_id IS NULL              THEN 'missing_from_projection'
           WHEN expected.transcript_id IS NULL            THEN 'absent_from_ledger'
           WHEN stored.status IS DISTINCT FROM expected.status THEN 'status_mismatch'
           ELSE 'shared_at_mismatch'
       END                                                    AS problem,
       stored.status                                          AS stored_status,
       expected.status                                        AS expected_status,
       stored.shared_at                                       AS stored_shared_at,
       expected.shared_at                                     AS expected_shared_at
FROM transcript_shares stored
FULL OUTER JOIN transcript_share_expected_state expected
  ON stored.transcript_id = expected.transcript_id
 AND stored.group_id      = expected.group_id
WHERE stored.transcript_id IS NULL
   OR expected.transcript_id IS NULL
   OR stored.status    IS DISTINCT FROM expected.status
   OR stored.shared_at IS DISTINCT FROM expected.shared_at;

-- Rebuilds the whole projection from the ledger and returns the row count it
-- installed. Takes an EXCLUSIVE lock so it cannot interleave with the
-- derivation trigger; run it in a maintenance window, not under load.
CREATE OR REPLACE FUNCTION rebuild_transcript_shares() RETURNS bigint
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE
    installed bigint;
BEGIN
    LOCK TABLE transcript_shares IN EXCLUSIVE MODE;
    PERFORM set_config('app.share_state_derivation', 'on', true);

    DELETE FROM transcript_shares;
    INSERT INTO transcript_shares (transcript_id, group_id, status, shared_at)
    SELECT transcript_id, group_id, status, shared_at FROM transcript_share_expected_state;
    GET DIAGNOSTICS installed = ROW_COUNT;

    PERFORM set_config('app.share_state_derivation', 'off', true);
    RETURN installed;
END $$;

-- How many rows of the projection disagree with the ledger. Zero is the only
-- healthy answer.
CREATE OR REPLACE FUNCTION check_transcript_shares_drift() RETURNS bigint
LANGUAGE sql STABLE SET search_path = pg_catalog, public AS $$
    SELECT count(*) FROM transcript_share_drift;
$$;

-- ---------------------------------------------------------------------------
-- Column meanings, attached to the schema so \d+ answers the question and a
-- reader never has to find a document to know what a column holds.
-- ---------------------------------------------------------------------------
COMMENT ON TABLE transcript_share_attempts IS
    'The ledger: one row per recorded event in a transcript''s relationship with a collective '
    '(submitted, decided, withdrawn, removed). transcript_shares is a projection of this table '
    'and is reconstructible from it with rebuild_transcript_shares().';
COMMENT ON COLUMN transcript_share_attempts.event_num IS
    'Orders the events within one (transcript, collective) pair, from 1. The highest event_num '
    'for a pair is the current one.';
COMMENT ON COLUMN transcript_share_attempts.recorded_at IS
    'When this event was recorded. For a submission that is when it was offered; a later decision '
    'on the same event does not change it (decided_at carries that).';
COMMENT ON TABLE transcript_shares IS
    'DERIVED current-state row, written only by trg_derive_transcript_share from '
    'transcript_share_attempts. Application code never writes it. Rebuild with '
    'rebuild_transcript_shares(); verify with check_transcript_shares_drift().';
COMMENT ON COLUMN transcript_shares.shared_at IS
    'The recorded_at of the CURRENT LATEST event for this (transcript, collective) pair - that is, '
    'when the submission behind the present state was made. It is NOT the first-ever submission '
    'for the pair: a rejected and resubmitted contribution carries the resubmission''s time, which '
    'is what keeps a moderation queue ordered by genuine age. It is also NOT the approval time; '
    'that is decided_at on the underlying event.';
COMMENT ON COLUMN transcript_shares.status IS
    'The current event''s status, restricted to the three representable states. A pair whose '
    'latest event is retracted or revoked has NO row here at all.';
