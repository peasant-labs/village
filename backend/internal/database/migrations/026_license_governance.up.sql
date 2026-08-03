-- Per-transcript license + fail-closed governance audit (data-rights system).
--
-- SUPERSEDES the never-merged migration 025 (both in-branch content generations —
-- see docs/deletion-data-lifecycle-model.md §6 and the frozen fixtures in
-- internal/database/testdata/migration_025_gen{1,2}.up.sql). 025's version number
-- is burned: branch-based databases recorded schema_migrations version=25 under two
-- DIFFERENT contents, and RunMigrations dedupes on the integer version alone. This
-- migration therefore registers as 026 (the 024→026 gap is fine; an 18→20 gap
-- already exists) and is written as a FIXPOINT: all DDL is guarded and ORDERED so
-- applying it to any environment class — fresh, old-025 (gen-1), new-025 (gen-2) —
-- converges on the identical final schema, and re-applying is a no-op. The stale
-- version=25 row is deliberately left in place as a forensic trace (ratified at
-- Plan UAT). Guarded DDL silently skips existing objects, so the compensating
-- control is TestMigration026_ConvergesAllEnvClasses (catalog-granularity diff:
-- columns, constraints, indexes, triggers, functions).
--
-- ORDERING RULE: old-generation objects are dropped FIRST, before any final-design
-- object that could collide with their names. Index and relation names share one
-- schema-wide namespace: gen-1 named idx_gov_events_transcript on its own table, so
-- that name must be freed before CREATE INDEX IF NOT EXISTS below — otherwise the
-- guard silently skips and the audit table ends up unindexed on exactly the
-- environment class this migration exists to repair.

-- (0) Old-025 (gen-1) reconciliation FIRST. Dropping the table drops its indexes,
-- freeing the idx_gov_events_transcript name for the audit index below. The gen-1
-- table was CASCADE-coupled and never had a writer; no data is lost.
DROP TABLE IF EXISTS transcript_governance_events;

-- (1) licenses — the home for license OBLIGATIONS. Obligations live here (keyed by
-- license id), never as columns on transcripts: on transcripts, license_id ->
-- obligations would be a non-key dependency (many transcripts share one license),
-- violating BCNF. Here id is the key and determines every column.
--
-- SOURCE OF TRUTH for the id set is peasant's pkg/schema, NOT this file: the `id`
-- values below ARE the typed constants schema.LicenseCC0 / LicenseCCBY /
-- LicenseCCBYSA. A .sql migration can't import Go, so they're repeated here as
-- literals — but they can't silently drift: the cross-repo guard in
-- migration_026_license_governance_integration_test.go pins this seed's id set
-- EXACTLY to schema.AllLicenses, so adding/removing a license in peasant fails CI
-- here until the seed follows.
--
-- The OBLIGATION columns are owned HERE by design — pkg/schema deliberately carries
-- only the id menu, and village owns license obligations. Every seeded row's full
-- obligation tuple is pinned by test (obligation drift is a wrong consent screen).
--
-- MODEL CEILING: attribution/share-alike/commercial cover the CC menu ONLY.
-- proprietary / unlicensed / *-ND (peasant#22) need new axes (e.g. redistribution_ok,
-- derivatives_ok) — expect an ALTER, not a reinterpretation of these three booleans.
--
-- NO RANK (supersedes gen-2's permissiveness_rank): collective license resolution
-- is DECIDED by the collective owner at creation and CONSENTED to at join time
-- (docs/collective-repository-connections.md). It is NOT computed from a
-- permissiveness order — licenses form a partial order on independent obligation
-- axes (CC-BY-NC and CC-BY-SA are incomparable), so no scalar total order is
-- coherent. The obligation booleans below are the axes the join-time consent
-- screen renders.
CREATE TABLE IF NOT EXISTS licenses (
    id                   TEXT PRIMARY KEY,
    name                 TEXT NOT NULL,
    url                  TEXT NOT NULL,
    attribution_required BOOLEAN NOT NULL,
    share_alike          BOOLEAN NOT NULL,
    commercial_ok        BOOLEAN NOT NULL
);
ALTER TABLE licenses DROP COLUMN IF EXISTS permissiveness_rank;  -- new-025 (gen-2) envs

INSERT INTO licenses (id, name, url, attribution_required, share_alike, commercial_ok) VALUES
    ('CC0-1.0',      'Creative Commons Zero v1.0 Universal',        'https://creativecommons.org/publicdomain/zero/1.0/', FALSE, FALSE, TRUE),
    ('CC-BY-4.0',    'Creative Commons Attribution 4.0',            'https://creativecommons.org/licenses/by/4.0/',        TRUE,  FALSE, TRUE),
    ('CC-BY-SA-4.0', 'Creative Commons Attribution-ShareAlike 4.0', 'https://creativecommons.org/licenses/by-sa/4.0/',     TRUE,  TRUE,  TRUE)
ON CONFLICT (id) DO NOTHING;

-- (2) Legal axis: the CURRENT license on each transcript. NULL = legacy/unset.
-- Partial index: license_id is mostly NULL (legacy rows), and lookups are
-- "transcripts WITH license X" — mirrors idx_transcripts_visibility (001).
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS license_id TEXT REFERENCES licenses(id);
CREATE INDEX IF NOT EXISTS idx_transcripts_license ON transcripts(license_id)
    WHERE license_id IS NOT NULL;

-- (3) Governance event-type taxonomy — a reference table (like licenses) plus a Go
-- constant mirror (governance_events.go), BY DECISION (Plan UAT): governance UIs
-- will read and extend these, and the set is data model in both languages. The
-- drift guard pins this seed == database.AllGovernanceEventTypes.
CREATE TABLE IF NOT EXISTS governance_event_types (
    id          TEXT PRIMARY KEY,
    description TEXT NOT NULL
);

INSERT INTO governance_event_types (id, description) VALUES
    ('published',          'Initial governance snapshot recorded when the transcript was first published'),
    ('license_changed',    'The transcript license was changed'),
    ('visibility_changed', 'The transcript visibility (privacy) was changed'),
    ('governance_changed', 'Both the license and visibility changed in a single action'),
    ('retracted',          'The transcript was withdrawn from publication')
ON CONFLICT (id) DO NOTHING;

-- (4) Append-only governance audit log. One row = a typed event carrying a full
-- policy SNAPSHOT (license_id + visibility), effective-dated, ordered by a
-- monotonic seq (effective_at is txn time: same-txn appends share a timestamp, so
-- "latest" = MAX(seq) per transcript).
--
-- DELETION SURVIVAL (docs/deletion-data-lifecycle-model.md §2 — RESOLVED YES,
-- ratified 2026-07-02): this is a LEGAL audit log; it outlives what it audits.
-- transcript_id and changed_by are RETAINED VALUES with NO FK — a transcript or
-- account can be hard-deleted and these events PERSIST. event_type and license_id
-- keep FKs to reference tables (seeded data, never deleted). Retaining changed_by
-- past account erasure relies on the GDPR legal-obligation carve-out — the lawful
-- basis is recorded in docs/deletion-data-lifecycle-model.md §7 (not just here).
CREATE TABLE IF NOT EXISTS transcript_governance_events_audit (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seq           BIGINT GENERATED ALWAYS AS IDENTITY,  -- monotonic append order; tiebreaker for "latest"
    transcript_id UUID NOT NULL,                        -- retained value; NO FK (audit survives transcript deletion)
    event_type    TEXT NOT NULL REFERENCES governance_event_types(id),
    license_id    TEXT REFERENCES licenses(id),
    -- The visibility menu is also CHECKed on transcripts (001). Migrations are
    -- immutable once shipped: adding a visibility tier = a NEW migration altering
    -- BOTH checks. If only the transcripts side is widened, the audit triggers
    -- below will reject the new value and BLOCK the mutation — see the
    -- "adding a visibility tier" checklist in AGENTS.md.
    visibility    VARCHAR(20) NOT NULL CHECK (visibility IN ('public', 'private', 'shared')),
    changed_by    UUID NOT NULL,                        -- retained value; NO FK (audit survives account deletion)
    effective_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_gov_events_transcript
    ON transcript_governance_events_audit(transcript_id, seq DESC);

-- (5) published + *_changed: ONE classifier function, two triggers. The audit log
-- has NO application writer — every event type is produced here, at the one layer
-- every mutation path crosses. AFTER triggers: pure side-effect writers (a BEFORE
-- trigger could accidentally mutate NEW).
--
-- FAIL-CLOSED ATTRIBUTION (ratified at Plan UAT): the actor comes from the
-- txn-local GUC app.actor_id (set via SET LOCAL / set_config(..., true) by the
-- application's inTxAs/inTxAsSystem helpers). There is NO owner fallback — a
-- guessed attribution is fabricated evidence in a legal log, so a mutation that
-- forgot the actor plumbing fails loudly instead of silently mis-attributing.
-- Sanctioned non-user mutations (seeds, backfills, ops runbooks) attribute to the
-- reserved SYSTEM ACTOR 00000000-0000-0000-0000-000000000000
-- (database.SystemActorID; app.* GUC registry in the lifecycle doc §7).
--
-- PRODUCTION HARDENING: run the API as a non-owner role and additionally
-- REVOKE UPDATE, DELETE ON transcript_governance_events_audit FROM <app_role>.
-- (REVOKE cannot bind the table owner — which is how integration tests connect —
-- hence the trigger-based enforcement in (8) below.)
CREATE OR REPLACE FUNCTION audit_transcript_governance() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE
    evt   TEXT;
    actor UUID;
BEGIN
    actor := NULLIF(current_setting('app.actor_id', true), '')::uuid;
    IF actor IS NULL THEN
        RAISE EXCEPTION 'governance audit requires app.actor_id (fail-closed): % on transcripts blocked. '
            'Route the mutation through inTxAs (authenticated user) or inTxAsSystem; raw SQL and '
            'sanctioned ops/backfills must SET LOCAL app.actor_id — system actor '
            '00000000-0000-0000-0000-000000000000 (see docs/deletion-data-lifecycle-model.md §7).', TG_OP;
    END IF;
    IF TG_OP = 'INSERT' THEN
        evt := 'published';
    ELSE  -- UPDATE; the trigger's WHEN clause guarantees a governance axis moved
        IF NEW.license_id IS DISTINCT FROM OLD.license_id
           AND NEW.visibility IS DISTINCT FROM OLD.visibility THEN
            evt := 'governance_changed';  -- one row per logical action, full snapshot
        ELSIF NEW.license_id IS DISTINCT FROM OLD.license_id THEN
            evt := 'license_changed';
        ELSE
            evt := 'visibility_changed';
        END IF;
    END IF;
    INSERT INTO transcript_governance_events_audit
        (transcript_id, event_type, license_id, visibility, changed_by)
    VALUES (NEW.id, evt, NEW.license_id, NEW.visibility, actor);
    RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS trg_audit_transcript_publish ON transcripts;
CREATE TRIGGER trg_audit_transcript_publish
    AFTER INSERT ON transcripts
    FOR EACH ROW EXECUTE FUNCTION audit_transcript_governance();

-- No-op suppression is structural: the WHEN clause means a title-only (or
-- content_hash-only, etc.) UPDATE never fires the trigger at all.
DROP TRIGGER IF EXISTS trg_audit_transcript_governance ON transcripts;
CREATE TRIGGER trg_audit_transcript_governance
    AFTER UPDATE ON transcripts
    FOR EACH ROW
    WHEN (OLD.license_id IS DISTINCT FROM NEW.license_id
       OR OLD.visibility  IS DISTINCT FROM NEW.visibility)
    EXECUTE FUNCTION audit_transcript_governance();

-- (6) retracted — the deletion-survival KEYSTONE. A transcript can be removed two
-- ways: DeleteTranscript (one row) and DeleteAccount, a DB-level ON DELETE CASCADE
-- from users (migration 010) that runs NO per-transcript application code. Only a
-- trigger sees both. BEFORE DELETE, FOR EACH ROW: fires per cascaded row too.
--
-- FAIL-CLOSED like the classifier: gen-2's COALESCE-to-the-row-owner fallback is
-- REMOVED. The DeleteAccount cascade inherits the deleting transaction's GUC
-- (SET LOCAL is txn-scoped and cascades run in the same txn), so the one inherent
-- multi-row path stays correct; a raw DELETE without the GUC is blocked —
-- deliberately.
CREATE OR REPLACE FUNCTION audit_transcript_retract() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE
    actor UUID;
BEGIN
    actor := NULLIF(current_setting('app.actor_id', true), '')::uuid;
    IF actor IS NULL THEN
        RAISE EXCEPTION 'governance audit requires app.actor_id (fail-closed): DELETE on transcripts blocked. '
            'Route the deletion through inTxAs (authenticated user) or inTxAsSystem; raw SQL and '
            'sanctioned ops must SET LOCAL app.actor_id — system actor '
            '00000000-0000-0000-0000-000000000000 (see docs/deletion-data-lifecycle-model.md §7).';
    END IF;
    INSERT INTO transcript_governance_events_audit
        (transcript_id, event_type, license_id, visibility, changed_by)
    VALUES (OLD.id, 'retracted', OLD.license_id, OLD.visibility, actor);
    RETURN OLD;
END $$;

DROP TRIGGER IF EXISTS trg_audit_transcript_retract ON transcripts;
CREATE TRIGGER trg_audit_transcript_retract
    BEFORE DELETE ON transcripts
    FOR EACH ROW EXECUTE FUNCTION audit_transcript_retract();

-- (7) Tamper resistance: the audit table is append-only AT THE DB LAYER. The
-- txn-scoped GUC escape exists because REVOKE cannot bind the table owner (the
-- role integration tests connect as) and the audit table has no FK cascade to
-- clean it — hermetic test teardown needs a sanctioned, deliberate, statement-log-
-- visible path (SET LOCAL app.audit_maintenance = 'on' inside a transaction).
CREATE OR REPLACE FUNCTION governance_audit_block_mutation() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF current_setting('app.audit_maintenance', true) = 'on' THEN
        RETURN COALESCE(OLD, NEW);
    END IF;
    RAISE EXCEPTION 'transcript_governance_events_audit is append-only: % blocked. This is the durable '
        'legal record (docs/deletion-data-lifecycle-model.md §2/§7). For sanctioned maintenance, run '
        'inside a transaction with SET LOCAL app.audit_maintenance = ''on'' and record why.', TG_OP;
END $$;

DROP TRIGGER IF EXISTS trg_governance_audit_immutable ON transcript_governance_events_audit;
CREATE TRIGGER trg_governance_audit_immutable
    BEFORE UPDATE OR DELETE ON transcript_governance_events_audit
    FOR EACH ROW EXECUTE FUNCTION governance_audit_block_mutation();
