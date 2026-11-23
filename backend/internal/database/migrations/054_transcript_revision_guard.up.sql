-- The transcript content-revision transition guard.
--
-- Migration 051 added the revision and the alternate identity slot, and the
-- Village writer decides a revision from the identity the row already stores.
-- Nothing in the database checked that decision, so an older binary could still
-- replace a pre-image and leave the revision unchanged. This migration makes the
-- database back the writer: every UPDATE that moves a content identity, the
-- stored object or the revision is compared with the row it replaces.
--
-- Terms. The primary pair is content_hash + blob_size_bytes. The segmented pair
-- is segmented_identity_hash + segmented_identity_size. A row is in one of five
-- identity states: `complete` (a canonical primary hash with a non-negative
-- size), `segmented` (the segmented pair), `hash_only` (a canonical primary hash
-- with no usable size), `malformed` (a primary hash that is not canonical) and
-- `free` (no hash at all). A replacement moves blob_key, the stored object.
--
-- Rules, in the order the function applies them:
--   1a  complete -> complete  equal pair keeps the revision; a different pair
--                             advances it by exactly one.
--   1b  segmented -> complete the same, and the segmented pair is cleared in the
--                             same statement (the slot-exclusive CHECK).
--   1c  complete -> segmented refused: a usable identity is never cleared.
--   1d  segmented -> segmented equal keeps the revision; a different pair, or a
--                             move of the object without a complete pair, is
--                             refused.
--   2a  free -> complete      on the SAME blob_key, an install: the revision is
--                             kept and a writer-supplied change is refused.
--   2b  free -> segmented     on the SAME blob_key, the same.
--   2c  hash_only -> complete on the SAME blob_key and the SAME hash, the same.
--   2d  malformed -> complete on the SAME blob_key, the same: the publish heal
--                             installs an authenticated identity over a hash
--                             that was never one.
--   3   free -> complete      on a NEW blob_key is refused, unless rule 4. A free row
--                             that stays free, or becomes hash_only or malformed,
--                             claims no identity and may move: only the revision
--                             must not change.
--   4   free -> complete      on a new blob_key with the revision kept, from a
--                             revision-aware writer: the approved baseline. It
--                             makes no equality claim.
--   5   a usable identity -> anything that is not a usable identity: refused.
--       Only a malformed pair may be cleared (malformed -> free, the same
--       blob_key, the revision kept): the operator repair.
--   6   an advance past 2^63-1 is refused.
--   7   wrapped_data_key, encryption_algorithm and key_version are not read: a
--       rewrap keeps the identity and the revision.
-- A different identity written by a writer that does not name content_revision
-- in its SET list, on a row that already had a complete or segmented identity,
-- is counted: the guard advances the revision by one. A writer that names the
-- column is a revision-aware writer and must supply the exact value.
--
-- What the database cannot know. A trigger sees the row before and after; it
-- cannot see how a writer learned what it wrote. Whether a writer authenticated
-- the old object, or classified a missing object as definite absence before it
-- asked for rule 4, is the writer's claim. The write fence, the typed storage
-- outcomes and review are the control for that claim; this guard backs the
-- arithmetic and the slot rules, and refuses the writers that do not know them.
--
-- The revision-aware signal. PostgreSQL fires `BEFORE UPDATE OF content_revision`
-- only for a statement that names the column in its SET list, even when the value
-- does not change. trg_transcript_writer_version_revision_aware fires first
-- (triggers fire in name order), records that fact in the transaction-local setting
-- app.transcript_revision_named, and the guard reads it and clears it before it
-- decides. The guard also fires for every column it reads, so the setting never
-- outlives the row it was set for.
--
-- Trigger names. Both names extend `trg_transcript_writer_version`, the 031/048
-- writer fence, so they fire after it: a statement with no writer marker is
-- refused by the fence with its own message before the guard reads the row. The
-- signal sorts before the guard (`_aware` before `_guard`).
--
-- Exceptions carry SQLSTATE TR054 and name the operation, the rule and the
-- identity states only: never an id, hash, object key, path, session id, owner
-- or content.
--
-- No INSERT rule: a create omits the revision and takes the default 1. The
-- migration is forward-only (see the down file).

CREATE OR REPLACE FUNCTION transcript_identity_state(
    primary_hash TEXT, primary_size BIGINT, segmented_hash TEXT, segmented_size BIGINT)
RETURNS TEXT
LANGUAGE sql IMMUTABLE
SET search_path = pg_catalog, public
AS $$
    SELECT CASE
        WHEN segmented_hash IS NOT NULL THEN 'segmented'
        WHEN primary_hash IS NULL THEN 'free'
        WHEN primary_hash !~ '^[0-9a-f]{64}$' THEN 'malformed'
        WHEN primary_size IS NULL OR primary_size < 0 THEN 'hash_only'
        ELSE 'complete'
    END
$$;

CREATE OR REPLACE FUNCTION refuse_transcript_revision_transition(
    rule TEXT, old_state TEXT, new_state TEXT, why TEXT)
RETURNS void
LANGUAGE plpgsql
SET search_path = pg_catalog, public
AS $$
BEGIN
    RAISE EXCEPTION USING
        ERRCODE = 'TR054',
        MESSAGE = format('transcript revision guard refused an UPDATE of transcripts in '
            'enforce_transcript_revision_transition (%s): %s. The row went from a %s identity to a %s '
            'identity and nothing was written.', rule, why, old_state, new_state),
        HINT = 'Publish with a Village writer that decides the revision from the stored identity and '
            'names content_revision. A pending, malformed or divergent transcript that a current writer '
            'cannot move is repaired by an operator (docs/database-invariants.md, operator content repair).';
END
$$;

CREATE OR REPLACE FUNCTION mark_transcript_revision_named() RETURNS TRIGGER
LANGUAGE plpgsql
SET search_path = pg_catalog, public
AS $$
BEGIN
    PERFORM set_config('app.transcript_revision_named', 'on', true);
    RETURN NEW;
END
$$;

CREATE OR REPLACE FUNCTION enforce_transcript_revision_transition() RETURNS TRIGGER
LANGUAGE plpgsql
SET search_path = pg_catalog, public
AS $$
DECLARE
    named BOOLEAN := COALESCE(current_setting('app.transcript_revision_named', true), '') = 'on';
    old_state TEXT := transcript_identity_state(
        OLD.content_hash, OLD.blob_size_bytes, OLD.segmented_identity_hash, OLD.segmented_identity_size);
    new_state TEXT := transcript_identity_state(
        NEW.content_hash, NEW.blob_size_bytes, NEW.segmented_identity_hash, NEW.segmented_identity_size);
    moved BOOLEAN := OLD.blob_key IS DISTINCT FROM NEW.blob_key;
    old_hash TEXT;
    old_size BIGINT;
    new_hash TEXT;
    new_size BIGINT;
    same_pair BOOLEAN;
    max_revision CONSTANT BIGINT := 9223372036854775807;
BEGIN
    -- Consume the signal first so that no exit path leaves it set.
    PERFORM set_config('app.transcript_revision_named', '', true);

    IF NEW.content_revision < OLD.content_revision THEN
        PERFORM refuse_transcript_revision_transition('revision', old_state, new_state,
            'the content revision never decreases');
    END IF;

    IF OLD.content_hash IS NOT DISTINCT FROM NEW.content_hash
       AND OLD.blob_size_bytes IS NOT DISTINCT FROM NEW.blob_size_bytes
       AND OLD.segmented_identity_hash IS NOT DISTINCT FROM NEW.segmented_identity_hash
       AND OLD.segmented_identity_size IS NOT DISTINCT FROM NEW.segmented_identity_size
       AND NOT moved THEN
        IF NEW.content_revision <> OLD.content_revision THEN
            PERFORM refuse_transcript_revision_transition('revision', old_state, new_state,
                'the content revision changed with no change of identity or object');
        END IF;
        RETURN NEW;
    END IF;

    IF old_state = 'segmented' THEN
        old_hash := OLD.segmented_identity_hash;
        old_size := OLD.segmented_identity_size;
    ELSE
        old_hash := OLD.content_hash;
        old_size := OLD.blob_size_bytes;
    END IF;
    IF new_state = 'segmented' THEN
        new_hash := NEW.segmented_identity_hash;
        new_size := NEW.segmented_identity_size;
    ELSE
        new_hash := NEW.content_hash;
        new_size := NEW.blob_size_bytes;
    END IF;
    same_pair := old_hash IS NOT DISTINCT FROM new_hash AND old_size IS NOT DISTINCT FROM new_size;

    IF old_state IN ('complete', 'segmented') THEN
        IF new_state = 'complete' THEN
            -- Rules 1a and 1b: the compared pair is the stored pre-image pair.
            IF same_pair THEN
                IF NEW.content_revision <> OLD.content_revision THEN
                    PERFORM refuse_transcript_revision_transition('rule 1a/1b', old_state, new_state,
                        'an equal identity keeps the content revision');
                END IF;
                RETURN NEW;
            END IF;
            IF OLD.content_revision = max_revision THEN
                PERFORM refuse_transcript_revision_transition('rule 6', old_state, new_state,
                    'the content revision cannot advance beyond its maximum');
            END IF;
            IF NEW.content_revision = OLD.content_revision + 1 THEN
                RETURN NEW;
            END IF;
            IF NEW.content_revision = OLD.content_revision AND moved AND NOT named THEN
                -- A replacement from a writer that does not know revisions: count it.
                NEW.content_revision := OLD.content_revision + 1;
                RETURN NEW;
            END IF;
            IF NOT moved THEN
                PERFORM refuse_transcript_revision_transition('rule 1a/1b', old_state, new_state,
                    'a different identity on the unchanged stored object is not a replacement and '
                    'an identity is never overwritten in place');
            END IF;
            PERFORM refuse_transcript_revision_transition('rule 1a/1b', old_state, new_state,
                'a different identity advances the content revision by exactly one');
        ELSIF old_state = 'segmented' AND new_state = 'segmented' THEN
            IF NEW.content_hash IS NOT NULL THEN
                PERFORM refuse_transcript_revision_transition('rule 1b', old_state, new_state,
                    'a complete identity replaces a segmented one only if the segmented pair is cleared '
                    'in the same statement');
            END IF;
            IF NOT same_pair THEN
                PERFORM refuse_transcript_revision_transition('rule 1d', old_state, new_state,
                    'a segmented identity never moves to a different identity');
            END IF;
            -- The identity is equal, so the statement can only have moved the object.
            PERFORM refuse_transcript_revision_transition('rule 1d', old_state, new_state,
                'a segmented identity never moves to a new stored object without a complete identity');
        ELSIF new_state = 'segmented' THEN
            PERFORM refuse_transcript_revision_transition('rule 1c', old_state, new_state,
                'a usable identity is never cleared in favour of a segmented one');
        END IF;
        PERFORM refuse_transcript_revision_transition('rule 5', old_state, new_state,
            'a usable identity is never cleared or reduced to an unusable one');
    END IF;

    IF old_state = 'hash_only' THEN
        IF new_state = 'complete' AND NOT moved AND NEW.content_hash = OLD.content_hash THEN
            -- Rule 2c: the install completes the same hash on the same object.
            IF NEW.content_revision <> OLD.content_revision THEN
                PERFORM refuse_transcript_revision_transition('rule 2c', old_state, new_state,
                    'completing an identity keeps the content revision and a writer-supplied change is refused');
            END IF;
            RETURN NEW;
        END IF;
        PERFORM refuse_transcript_revision_transition('rule 2c', old_state, new_state,
            'only the same hash on the same stored object may complete a hash-only identity');
    END IF;

    IF old_state = 'malformed' THEN
        IF NOT moved AND NEW.content_revision = OLD.content_revision AND new_state IN ('free', 'complete') THEN
            -- Rule 5's sanctioned clear (to free) and rule 2d's install (to complete).
            RETURN NEW;
        END IF;
        PERFORM refuse_transcript_revision_transition('rule 2d', old_state, new_state,
            'a malformed identity is cleared or replaced only on the same stored object with the revision kept');
    END IF;

    -- old_state = 'free'
    IF new_state IN ('free', 'hash_only', 'malformed') THEN
        -- No usable identity is claimed, so there is no pair to compare and no
        -- revision to count: only the revision itself must stand still.
        IF NEW.content_revision <> OLD.content_revision THEN
            PERFORM refuse_transcript_revision_transition('rule 2a', old_state, new_state,
                'a transcript with no usable identity cannot advance its content revision');
        END IF;
        RETURN NEW;
    END IF;
    IF new_state = 'complete' THEN
        IF NOT moved THEN
            IF NEW.content_revision <> OLD.content_revision THEN
                PERFORM refuse_transcript_revision_transition('rule 2a', old_state, new_state,
                    'installing an identity keeps the content revision and a writer-supplied change is refused');
            END IF;
            RETURN NEW;
        END IF;
        -- Rule 4 is evaluated before rule 3 refuses.
        IF named AND NEW.content_revision = OLD.content_revision THEN
            RETURN NEW;
        END IF;
        PERFORM refuse_transcript_revision_transition('rule 3', old_state, new_state,
            'a transcript with no recorded identity is replaced only by a writer that names '
            'content_revision and keeps it; an older writer would leave no identity to compare');
    ELSIF new_state = 'segmented' THEN
        IF NOT moved AND NEW.content_revision = OLD.content_revision THEN
            RETURN NEW;
        END IF;
        PERFORM refuse_transcript_revision_transition('rule 2b', old_state, new_state,
            'a segmented identity is installed on the unchanged stored object with the revision kept');
    END IF;
    -- Unreachable: a free row's new state is free, hash_only, malformed, complete or segmented.
    RETURN NEW;
END
$$;

-- The two triggers fire in name order: the signal first, the guard second.
CREATE TRIGGER trg_transcript_writer_version_revision_aware
BEFORE UPDATE OF content_revision
ON transcripts
FOR EACH ROW
EXECUTE FUNCTION mark_transcript_revision_named();

CREATE TRIGGER trg_transcript_writer_version_revision_guard
BEFORE UPDATE OF content_hash, blob_size_bytes, segmented_identity_hash, segmented_identity_size,
                 content_revision, blob_key
ON transcripts
FOR EACH ROW
EXECUTE FUNCTION enforce_transcript_revision_transition();
