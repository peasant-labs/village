-- Owner corrections to derived, published metadata.
--
-- A correction is stored SEPARATELY from the published row it corrects: the
-- transcript keeps the exact bytes its harness reported, and the owner's
-- preferred rendering lives here. Nothing in this table rewrites history.
--
-- target_kind and field carry the full reserved menus so adding a correctable
-- field later is a code change, not a CHECK migration. Only the pair
-- ('project', 'display_name') is writable in the application today; a typed Go
-- enum at the handler is what narrows the menu.
--
-- No governance audit trigger, and no app.actor_id requirement. The governance
-- audit (migration 026) fires only on transcripts.license_id and
-- transcripts.visibility, which are the disclosure axes; a display name the
-- owner chooses for their own project is neither, and the shipped project
-- rename path is already actor-less for the same reason. The reversing
-- condition is recorded in docs/database-invariants.md: if the reserved
-- 'redaction_decision' field is ever implemented, that IS a disclosure axis and
-- the audit decision is re-opened BEFORE the migration that implements it.
CREATE TABLE owner_overrides (
    owner_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_kind VARCHAR(32) NOT NULL
                 CHECK (target_kind IN ('project', 'transcript', 'redaction_span')),
    target_key  TEXT        NOT NULL,
    field       VARCHAR(32) NOT NULL
                 CHECK (field IN ('display_name', 'title', 'redaction_decision')),
    value       TEXT        NOT NULL CHECK (char_length(value) BETWEEN 1 AND 4096),
    provenance  JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, target_kind, target_key, field)
);

CREATE INDEX idx_owner_overrides_owner_kind ON owner_overrides (owner_id, target_kind);
