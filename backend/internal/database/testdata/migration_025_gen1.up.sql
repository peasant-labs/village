-- FROZEN HISTORICAL FIXTURE — gen-1 of the never-merged migration 025, as committed
-- at fd5e162 (branch iteration before the in-branch rewrite). Used ONLY by
-- TestMigration026_ConvergesAllEnvClasses to reproduce the old-025 environment class.
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

-- (3) Append-only governance history. One row = a full policy SNAPSHOT (legal
-- axis: license_id; privacy axis: visibility), effective-dated. Powers "change
-- governance later": tightening appends a NEW event and never claws back. The
-- current transcripts.{license_id, visibility} are an app-maintained cache of
-- the latest event (written in the same txn, by future app code).
--
-- Ordering is by `seq` (a monotonic IDENTITY), NOT by effective_at. effective_at
-- defaults to now(), which is TRANSACTION time, so two events appended in one
-- txn share a timestamp: an earlier UNIQUE(transcript_id, effective_at) both
-- collided on same-txn appends AND wrongly forbade two legitimate same-instant
-- changes. `seq` gives a deterministic total order; "latest" = MAX(seq) per
-- transcript. Two events at the same effective_at are intentionally allowed.
--
-- SCOPE: the FKs below keep their ORIGINAL behavior on purpose — transcript_id
-- ON DELETE CASCADE, changed_by a plain reference. Whether this audit history
-- must SURVIVE transcript/account deletion (and the soft-delete / erasure model
-- that would require — auth, S3, PII) is deliberately OUT OF SCOPE here and is
-- designed separately: see docs/deletion-data-lifecycle-model.md. The table is
-- dormant today (no writer yet), so the current FK behavior is harmless until
-- that design lands.
CREATE TABLE transcript_governance_events (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seq           BIGINT GENERATED ALWAYS AS IDENTITY,  -- monotonic append order; tiebreaker for "latest"
    transcript_id UUID NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    license_id    TEXT REFERENCES licenses(id),
    visibility    VARCHAR(20) NOT NULL CHECK (visibility IN ('public', 'private', 'shared')),
    changed_by    UUID NOT NULL REFERENCES users(id),
    effective_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_gov_events_transcript ON transcript_governance_events(transcript_id, seq DESC);
