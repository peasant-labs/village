-- FROZEN HISTORICAL FIXTURE — gen-2 of the never-merged migration 025, as committed
-- at 193b0f2 (the PR-26 head superseded by migration 026). Used ONLY by
-- TestMigration026_ConvergesAllEnvClasses to reproduce the new-025 environment class.
-- DO NOT EDIT: this reproduces what those environments actually ran.
-- Per-transcript license + governance history (data-rights system, phase 1).
--
-- Adds the LEGAL axis (license) alongside the existing PRIVACY axis
-- (transcripts.visibility, from 001). License and privacy are INDEPENDENT
-- settings. Collective-level license resolution (the "meet" over a collective's
-- members) is deferred; the permissiveness_rank seeded here is its future input.

-- (1) Licenses reference table — the home for license OBLIGATIONS. Obligations
-- live here (keyed by license id), never as columns on transcripts: on
-- transcripts, license_id -> obligations would be a non-key dependency (many
-- transcripts share one license), violating BCNF. Here id is the key and
-- determines every column; permissiveness_rank is also UNIQUE (a 2nd candidate
-- key), so the table is in BCNF.
CREATE TABLE licenses (
    id                   TEXT PRIMARY KEY,
    name                 TEXT NOT NULL,
    url                  TEXT NOT NULL,
    attribution_required BOOLEAN NOT NULL,
    share_alike          BOOLEAN NOT NULL,
    commercial_ok        BOOLEAN NOT NULL,
    permissiveness_rank  INTEGER NOT NULL UNIQUE   -- total order for the collective "meet" (later phase)
);

-- Seed the closed three-tier menu. ON CONFLICT keeps the migration replay-safe.
--
-- SOURCE OF TRUTH for the id set is peasant's pkg/schema, NOT this file: the `id`
-- values below ARE the typed constants schema.LicenseCC0 / LicenseCCBY /
-- LicenseCCBYSA. A .sql migration can't import Go, so they're repeated here as
-- literals — but they can't silently drift: the cross-repo guard in
-- migration_025_license_governance_integration_test.go pins this seed's id set
-- EXACTLY (ordered) to schema.AllLicenses, so adding/removing a license in peasant
-- fails CI here until the seed follows.
--
-- The OBLIGATION columns (name, url, attribution_required, share_alike,
-- commercial_ok, permissiveness_rank) are owned HERE by design — pkg/schema
-- deliberately carries only the id menu (see its License doc comment), and village
-- owns license obligations. So those values are intentionally not derivable from
-- peasant; only the id set is, and it is guarded above.
INSERT INTO licenses (id, name, url, attribution_required, share_alike, commercial_ok, permissiveness_rank) VALUES
    ('CC0-1.0',      'Creative Commons Zero v1.0 Universal',        'https://creativecommons.org/publicdomain/zero/1.0/', FALSE, FALSE, TRUE, 0),
    ('CC-BY-4.0',    'Creative Commons Attribution 4.0',            'https://creativecommons.org/licenses/by/4.0/',        TRUE,  FALSE, TRUE, 1),
    ('CC-BY-SA-4.0', 'Creative Commons Attribution-ShareAlike 4.0', 'https://creativecommons.org/licenses/by-sa/4.0/',     TRUE,  TRUE,  TRUE, 2)
ON CONFLICT (id) DO NOTHING;

-- (2) Legal axis: the CURRENT license on each transcript. NULL = legacy/unset
-- (rows that predate licensing; new publishes set it at the app layer).
ALTER TABLE transcripts ADD COLUMN license_id TEXT REFERENCES licenses(id);

-- Partial index: license_id is mostly NULL (legacy rows), and lookups are
-- "transcripts WITH license X" — mirrors idx_transcripts_visibility (001).
CREATE INDEX idx_transcripts_license ON transcripts(license_id)
    WHERE license_id IS NOT NULL;

-- (3) Governance event-type taxonomy — the closed, extensible set of WHAT can
-- occur to a transcript's governance. A reference table (like licenses) so the
-- audit log records WHICH kind of event each row is, the set is documented in one
-- place, and new types are added by INSERT (not a schema change). The id IS the
-- event name (self-documenting + readable in FKs; mirrors licenses.id rather than
-- an opaque serial/uuid).
CREATE TABLE governance_event_types (
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
-- DECIDED (writer semantics): one row per logical action, carrying the full
-- post-change snapshot. A single action that moves BOTH axes is recorded as ONE
-- 'governance_changed' row (not two), so a reader never sees a half-snapshot;
-- the per-axis types remain for single-axis changes. See
-- docs/deletion-data-lifecycle-model.md.

-- (4) Append-only governance audit log. One row = a typed event (event_type)
-- carrying a full policy SNAPSHOT (legal axis: license_id; privacy axis:
-- visibility), effective-dated. Powers "change governance later": tightening
-- appends a NEW event and never claws back. The current transcripts.{license_id,
-- visibility} are an app-maintained cache of the latest event (written in the
-- same txn, by future app code).
--
-- Ordering is by `seq` (a monotonic IDENTITY), NOT by effective_at. effective_at
-- defaults to now(), which is TRANSACTION time, so two events appended in one
-- txn share a timestamp: an earlier UNIQUE(transcript_id, effective_at) both
-- collided on same-txn appends AND wrongly forbade two legitimate same-instant
-- changes. `seq` gives a deterministic total order; "latest" = MAX(seq) per
-- transcript. Two events at the same effective_at are intentionally allowed.
--
-- DELETION SURVIVAL: this is a LEGAL audit log, so it must outlive the things it
-- audits. transcript_id and changed_by are therefore RETAINED VALUES with NO
-- foreign key — a transcript or account can be hard-deleted and these events
-- PERSIST (the prior ON DELETE CASCADE would have wiped them, failing the
-- keep-every-event requirement). event_type and license_id keep their FKs to
-- reference tables (governance_event_types / licenses), which are seeded data that
-- is never deleted. Trade-offs (see docs/deletion-data-lifecycle-model.md):
--   (a) no DB-enforced referential integrity on insert — fine for an append-only
--       audit; the writer always inserts in the same txn as a real transcript;
--   (b) retaining changed_by needs a documented lawful basis under GDPR (the
--       legal-obligation carve-out over right-to-erasure);
--   (c) after a FULL account erasure the bare changed_by id no longer resolves to
--       a person — preserving "who, by name, forever" would need an actor snapshot
--       or soft-deleted users (a later project).
CREATE TABLE transcript_governance_events_audit (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seq           BIGINT GENERATED ALWAYS AS IDENTITY,  -- monotonic append order; tiebreaker for "latest"
    transcript_id UUID NOT NULL,                        -- retained value; NO FK (audit survives transcript deletion)
    event_type    TEXT NOT NULL REFERENCES governance_event_types(id),  -- reference data, never deleted → FK kept
    license_id    TEXT REFERENCES licenses(id),         -- reference data, never deleted → FK kept
    visibility    VARCHAR(20) NOT NULL CHECK (visibility IN ('public', 'private', 'shared')),
    changed_by    UUID NOT NULL,                        -- retained value; NO FK (audit survives account deletion)
    effective_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_gov_events_transcript ON transcript_governance_events_audit(transcript_id, seq DESC);

-- (5) RETRACTED-on-delete via a BEFORE DELETE trigger — the deletion-survival
-- KEYSTONE. A transcript can be removed two ways: DeleteTranscript (one row) and
-- DeleteAccount, which is a DB-level ON DELETE CASCADE from users (migration 010)
-- that runs NO per-transcript application code. Application instrumentation
-- therefore CANNOT guarantee a final 'retracted' event on every delete path —
-- only a DB trigger can. This BEFORE DELETE trigger appends the terminal snapshot
-- from OLD.*, so EVERY transcript exit is audited: the single delete, the cascaded
-- account deletion, and any future raw-SQL/admin delete.
--
-- ACTOR: the deleting user is supplied via `SET LOCAL app.actor_id = '<uuid>'`
-- before the DELETE (both DeleteTranscript and DeleteAccount set it). If it is
-- unset (e.g. a raw-SQL delete that forgot to), changed_by falls back to
-- OLD.owner_id — always a real, non-NULL UUID, so the NOT NULL holds and the actor
-- is never lost. The row is a full snapshot (OLD.license_id + OLD.visibility).
CREATE FUNCTION audit_transcript_retract() RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO transcript_governance_events_audit
        (transcript_id, event_type, license_id, visibility, changed_by)
    VALUES (
        OLD.id, 'retracted', OLD.license_id, OLD.visibility,
        COALESCE(NULLIF(current_setting('app.actor_id', true), '')::uuid, OLD.owner_id)
    );
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_audit_transcript_retract
    BEFORE DELETE ON transcripts
    FOR EACH ROW EXECUTE FUNCTION audit_transcript_retract();
