-- 052: the operator content repair ledger. One append-only row per repair attempt
-- and per terminal outcome. It is not a disclosure axis, not rewrite lineage and
-- not a governance event: it records a privileged action on a transcript's stored
-- content, by whom, under which recorded authorization, with which identity and
-- with what result.
CREATE TABLE transcript_content_repairs (
    repair_id                UUID        NOT NULL,
    seq                      BIGINT      GENERATED ALWAYS AS IDENTITY,
    phase                    TEXT        NOT NULL,
    action                   TEXT        NOT NULL,
    transcript_id            UUID        NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    operator_actor           UUID        NOT NULL,
    authorization_ref        TEXT        NOT NULL,
    reason                   TEXT        NOT NULL,
    prior_revision           BIGINT      NOT NULL,
    prior_identity_hash      TEXT,
    prior_identity_size      BIGINT,
    resulting_revision       BIGINT      NOT NULL,
    resulting_identity_hash  TEXT,
    resulting_identity_size  BIGINT,
    failure_class            TEXT,
    reconciles_repair_id     UUID,
    recorded_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repair_id, phase),
    CONSTRAINT transcript_content_repairs_phase_closed
        CHECK (phase IN ('attempted', 'applied', 'refused')),
    CONSTRAINT transcript_content_repairs_action_closed
        CHECK (action IN ('clear_malformed_identity', 'restore_attested_generation',
                          'replace_third_value')),
    -- Three-valued safe by construction: `failure_class IN (...)` is NULL when the
    -- column is NULL, and a CHECK accepts NULL, so `IS NOT NULL` is tested FIRST and
    -- makes the branch FALSE. A refused row without a class is refused, as
    -- repair-audit-failure-class-closed requires. The set is closed at six.
    CONSTRAINT transcript_content_repairs_failure_closed CHECK (
        (phase = 'refused'
            AND failure_class IS NOT NULL
            AND failure_class IN ('precondition_mismatch', 'object_mismatch', 'object_uncertain',
                                  'guard_refused', 'writer_fence_refused', 'object_write_failed'))
        OR (phase <> 'refused' AND failure_class IS NULL)),
    -- An attempt and a refusal attest nothing new: the transcript still holds the
    -- prior pair at the prior revision, so that is what they record. Only an applied
    -- row carries an action's effect. This is what makes a truthful refusal of a
    -- replacement (X,7 -> X,7) and of a clear on a valid-A row (A,R -> A,R) legal,
    -- and an unattested hash or an uncommitted revision illegal.
    CONSTRAINT transcript_content_repairs_action_identity CHECK (
        (phase = 'applied'
            AND ((action = 'clear_malformed_identity'
                    AND prior_identity_hash IS NULL AND prior_identity_size IS NULL
                    AND resulting_identity_hash IS NULL AND resulting_identity_size IS NULL
                    AND resulting_revision = prior_revision)
                 OR (action = 'restore_attested_generation'
                    AND prior_identity_hash IS NOT NULL
                    AND prior_identity_size IS NOT NULL
                    AND resulting_identity_hash IS NOT NULL
                    AND resulting_identity_size IS NOT NULL
                    AND resulting_identity_hash = prior_identity_hash
                    AND resulting_identity_size = prior_identity_size
                    AND resulting_revision = prior_revision)
                 OR (action = 'replace_third_value'
                    AND resulting_identity_hash IS NOT NULL
                    AND resulting_identity_size IS NOT NULL
                    AND (resulting_identity_hash, resulting_identity_size)
                        IS DISTINCT FROM (prior_identity_hash, prior_identity_size)
                    AND resulting_revision = prior_revision + 1)))
        OR (phase IN ('attempted', 'refused')
            AND (resulting_identity_hash, resulting_identity_size)
                IS NOT DISTINCT FROM (prior_identity_hash, prior_identity_size)
            AND resulting_revision = prior_revision)),
    -- Only a restore writes its attempt before touching the object, because only a
    -- restore writes an object. A clear or a replacement that cannot commit with its
    -- row changes nothing, so it writes no attempt.
    CONSTRAINT transcript_content_repairs_attempted_is_restore CHECK (
        phase <> 'attempted' OR action = 'restore_attested_generation'),
    CONSTRAINT transcript_content_repairs_hash_shape CHECK (
        (prior_identity_hash IS NULL OR prior_identity_hash ~ '^[0-9a-f]{64}$')
        AND (resulting_identity_hash IS NULL OR resulting_identity_hash ~ '^[0-9a-f]{64}$')),
    -- An identity is a pair, never half of one. A hash with a NULL size (or the
    -- reverse) is not an identity, and a half-filled pair would make the ordinary
    -- `=` in the restore branch evaluate NULL and pass, since a CHECK accepts NULL.
    -- Both pairs are therefore all-NULL or fully non-NULL, which is what lets the
    -- restore and attempt/refusal equalities be decided rather than evaded.
    CONSTRAINT transcript_content_repairs_identity_paired CHECK (
        (prior_identity_hash IS NULL) = (prior_identity_size IS NULL)
        AND (resulting_identity_hash IS NULL) = (resulting_identity_size IS NULL)),
    CONSTRAINT transcript_content_repairs_size_nonnegative CHECK (
        (prior_identity_size IS NULL OR prior_identity_size >= 0)
        AND (resulting_identity_size IS NULL OR resulting_identity_size >= 0)),
    CONSTRAINT transcript_content_repairs_revisions_positive CHECK (
        prior_revision >= 1 AND resulting_revision >= 1),
    CONSTRAINT transcript_content_repairs_authorization_bounded CHECK (
        char_length(btrim(authorization_ref)) BETWEEN 1 AND 256),
    CONSTRAINT transcript_content_repairs_reason_bounded CHECK (
        char_length(btrim(reason)) BETWEEN 1 AND 1000),
    CONSTRAINT transcript_content_repairs_reconciles_not_self CHECK (
        reconciles_repair_id IS NULL OR reconciles_repair_id <> repair_id),
    CONSTRAINT transcript_content_repairs_actor_is_not_system CHECK (
        operator_actor <> '00000000-0000-0000-0000-000000000000'::uuid)
);

-- One terminal outcome per attempt, enforced by construction: append-only logs are
-- never repaired in place, and a second outcome is a new attempt that cites this one.
CREATE UNIQUE INDEX transcript_content_repairs_one_terminal
    ON transcript_content_repairs (repair_id) WHERE phase <> 'attempted';
CREATE INDEX transcript_content_repairs_transcript_seq
    ON transcript_content_repairs (transcript_id, seq DESC);

-- Attribution is derived from the transaction-local GUC, never taken from the
-- statement (the same fail-closed model as the governance audit triggers).
CREATE OR REPLACE FUNCTION repair_audit_assign_actor() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE actor UUID;
BEGIN
    actor := NULLIF(current_setting('app.actor_id', true), '')::uuid;
    IF actor IS NULL THEN
        RAISE EXCEPTION 'content repair audit requires app.actor_id (fail-closed): % on '
            'transcript_content_repairs blocked. Set the operator''s account id with SET LOCAL '
            'app.actor_id; the system actor is refused and no repair may be unattributed.', TG_OP;
    END IF;
    IF NEW.operator_actor IS NOT NULL AND NEW.operator_actor <> actor THEN
        RAISE EXCEPTION 'content repair audit refuses a statement-supplied operator_actor that '
            'disagrees with app.actor_id: the actor is derived, never asserted.';
    END IF;
    NEW.operator_actor := actor;
    RETURN NEW;
END $$;

CREATE TRIGGER trg_repair_audit_actor
    BEFORE INSERT ON transcript_content_repairs
    FOR EACH ROW EXECUTE FUNCTION repair_audit_assign_actor();

-- Append-only under the application: an outcome is never rewritten.
CREATE OR REPLACE FUNCTION repair_audit_block_update() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    RAISE EXCEPTION 'transcript_content_repairs is append-only: UPDATE of repair % (%) blocked. '
        'Record a new repair_id and cite this one in reconciles_repair_id.', OLD.repair_id, OLD.phase;
END $$;

CREATE TRIGGER trg_repair_audit_immutable
    BEFORE UPDATE ON transcript_content_repairs
    FOR EACH ROW EXECUTE FUNCTION repair_audit_block_update();

-- Append-only against a direct DELETE too, without blocking the one sanctioned
-- deleter. The trigger cannot tell the two apart by privilege (Village runs a
-- combined runtime and migration role that owns this table, so REVOKE binds
-- nothing here) and cannot use a caller-settable GUC as a barrier. It uses the one
-- fact a direct delete cannot fake: the FK cascade runs as an AFTER action of the
-- parent's own deletion, so the transcript row is already gone when these rows are
-- removed, while a direct delete always sees its transcript still present. This is
-- the same predicate and the same reason as migration 036's
-- trg_transcript_shares_fail_closed on transcript_shares
-- (docs/database-invariants.md §6).
--
-- Scope of the claim, stated because a row trigger has a boundary: this fires per
-- row the DELETE actually matches. A DELETE whose predicate matches no row invokes
-- no row trigger and reports DELETE 0 - it removes nothing, so the append-only
-- guarantee is untouched and the statement is not a failure. No statement-level
-- trigger is added: the only guarantee it could add is "every DELETE statement on
-- this table fails", which protects no row, and it would turn an operator's no-op
-- cleanup into an error.
CREATE OR REPLACE FUNCTION repair_audit_block_delete() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM transcripts WHERE id = OLD.transcript_id) THEN
        RETURN OLD;   -- the transcript's own ON DELETE CASCADE; the parent is already gone
    END IF;
    RAISE EXCEPTION
        'transcript_content_repairs is append-only: DELETE of repair % (phase %) refused in '
        'repair_audit_block_delete because its transcript still exists, so this is a direct delete '
        'and not the transcript''s ON DELETE CASCADE. The ledger is the record of a privileged '
        'content repair and has no delete path. Delete the transcript when its retention ends - the '
        'cascade removes its repair rows - or record a new repair_id that cites this one in '
        'reconciles_repair_id (docs/database-invariants.md, operator content repair).',
        OLD.repair_id, OLD.phase;
END $$;

CREATE TRIGGER trg_repair_audit_delete_guard
    BEFORE DELETE ON transcript_content_repairs
    FOR EACH ROW EXECUTE FUNCTION repair_audit_block_delete();

-- Only a restore writes an attempt, and restoring requires an established
-- identity. An identity-free attempt cannot truthfully describe that action.
ALTER TABLE transcript_content_repairs
    ADD CONSTRAINT transcript_content_repairs_attempt_has_identity CHECK (
        phase <> 'attempted'
        OR (prior_identity_hash IS NOT NULL AND prior_identity_size IS NOT NULL));

-- Only terminal rows can resolve an earlier attempt by citation.
CREATE INDEX transcript_content_repairs_citations
    ON transcript_content_repairs (reconciles_repair_id)
    WHERE phase <> 'attempted';
