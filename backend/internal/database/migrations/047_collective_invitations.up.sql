-- Collective invitations, membership provenance, and the notification email.
--
-- An owner can no longer make a person a member without their consent: the
-- owner invites, and the person accepts or declines. Whether a membership was
-- consented to is what lets a collective's owners see that person's profile
-- (ListPublicOwnersForViewer), so every membership now records how it came to
-- exist.

-- ---------------------------------------------------------------------------
-- group_members.origin
--
--   creator     the account that created the collective, written by create.
--   self_join   the person joined an open or verified collective themselves.
--   invitation  the person accepted an owner's invitation.
--   legacy      the membership predates this migration, so how it came to
--               exist is unknown. An owner may have added the person by
--               username, so a legacy row is never treated as consent.
--
-- Every existing row is legacy except the creator's own owner row, which can
-- only have been written by create. The default exists only for that backfill
-- and is dropped, so every later insert must state its origin.
-- ---------------------------------------------------------------------------
ALTER TABLE group_members
    ADD COLUMN origin TEXT NOT NULL DEFAULT 'legacy'
    CONSTRAINT group_members_origin_check
        CHECK (origin IN ('creator', 'self_join', 'invitation', 'legacy'));

UPDATE group_members gm
SET origin = 'creator'
FROM groups g
WHERE g.id = gm.group_id
  AND gm.role = 'owner'
  AND gm.user_id = g.created_by;

ALTER TABLE group_members ALTER COLUMN origin DROP DEFAULT;

ALTER TABLE group_members
    ADD CONSTRAINT group_members_creator_is_owner
        CHECK (origin <> 'creator' OR role = 'owner');

-- The origin is written once. The one permitted change is legacy to
-- invitation: a member whose membership predates invitations accepted an
-- invitation, and so consented. A role change keeps the origin.
CREATE OR REPLACE FUNCTION guard_group_member_origin() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF OLD.origin = 'legacy' AND NEW.origin = 'invitation' THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'group_members.origin is written once: a % membership cannot become %. Only a '
        'legacy membership may become an invitation one, when the member accepts an invitation '
        '(docs/database-invariants.md, membership provenance).',
        OLD.origin, NEW.origin;
END $$;

DROP TRIGGER IF EXISTS trg_group_member_origin_write_once ON group_members;
CREATE TRIGGER trg_group_member_origin_write_once
    BEFORE UPDATE ON group_members
    FOR EACH ROW
    WHEN (OLD.origin IS DISTINCT FROM NEW.origin)
    EXECUTE FUNCTION guard_group_member_origin();

-- ---------------------------------------------------------------------------
-- collective_invitations: one row per open invitation.
--
-- invited_username is the handle the owner typed, lowercased. It is stored for
-- every invitation, whether or not an account holds it, and it is the only
-- invitee field an owner ever sees. invitee_id is the account that held the
-- handle when the invitation was made or last re-sent, or NULL when none did;
-- SET NULL when that account is deleted leaves an inert row that only expires,
-- is revoked, or is re-sent. invited_by is SET NULL too: the invitation
-- belongs to the collective.
--
-- Accepting and revoking delete the row (the membership's origin is the
-- record). A declined row stays, unchanged to the owner, until it expires.
-- Expiry is read-time (expires_at <= now()); an expired row is deleted by the
-- next invitation for the same collective and handle.
-- ---------------------------------------------------------------------------
CREATE TABLE collective_invitations (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id         UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    invitee_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    invited_username TEXT NOT NULL
        CONSTRAINT collective_invitations_username_lowercase
            CHECK (invited_username <> '' AND invited_username = lower(invited_username)),
    invited_by       UUID REFERENCES users(id) ON DELETE SET NULL,
    status           TEXT NOT NULL
        CONSTRAINT collective_invitations_status_check
            CHECK (status IN ('pending', 'declined')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL,
    CONSTRAINT collective_invitations_expiry_after_creation CHECK (expires_at > created_at)
);

-- One open invitation per collective and handle.
CREATE UNIQUE INDEX idx_collective_invitations_target
    ON collective_invitations (group_id, invited_username);

-- The invitee's own list.
CREATE INDEX idx_collective_invitations_invitee
    ON collective_invitations (invitee_id)
    WHERE invitee_id IS NOT NULL;

-- Deleting an account sets invited_by NULL on the invitations it sent; this
-- index keeps that lookup off a full scan.
CREATE INDEX idx_collective_invitations_invited_by
    ON collective_invitations (invited_by)
    WHERE invited_by IS NOT NULL;

-- ---------------------------------------------------------------------------
-- user_notification_emails: a person's one notification address.
--
-- Contact data is kept out of users on purpose: several user queries return
-- the whole row, so a column there would reach responses that must never carry
-- an address. The address is returned only in its owner's own settings.
--
-- address is NULL when the person has none; email_invitations is their choice
-- to be emailed invitations, kept even without an address so an opt-out
-- survives removing and re-adding one. verify_token_hash is the SHA-256 of the
-- one-time verification token, never the token itself.
-- ---------------------------------------------------------------------------
CREATE TABLE user_notification_emails (
    user_id           UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    address           TEXT
        CONSTRAINT user_notification_emails_address_length
            CHECK (address IS NULL OR char_length(address) BETWEEN 3 AND 254),
    verified_at       TIMESTAMPTZ,
    verify_token_hash BYTEA,
    verify_expires_at TIMESTAMPTZ,
    email_invitations BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_notification_emails_nothing_without_address
        CHECK (address IS NOT NULL
               OR (verified_at IS NULL AND verify_token_hash IS NULL AND verify_expires_at IS NULL)),
    CONSTRAINT user_notification_emails_token_has_expiry
        CHECK ((verify_token_hash IS NULL) = (verify_expires_at IS NULL))
);

-- ---------------------------------------------------------------------------
-- user_daily_quotas: the daily limits on invitations and emails, counted in
-- the database so a restart or a second instance cannot reset them.
--
--   invitation_sent      new invitations an owner sends (20 a day).
--   invitation_email     invitation emails a person receives (3 a day).
--   verification_email   verification emails an account requests (5 a day).
--
-- One row per account, limit and UTC day. The count is taken with one
-- conditional upsert, so concurrent requests cannot both pass the last slot.
-- Rows older than yesterday are deleted by the next count.
-- ---------------------------------------------------------------------------
CREATE TABLE user_daily_quotas (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    quota   TEXT NOT NULL
        CONSTRAINT user_daily_quotas_quota_check
            CHECK (quota IN ('invitation_sent', 'invitation_email', 'verification_email')),
    day     DATE NOT NULL,
    used    INTEGER NOT NULL
        CONSTRAINT user_daily_quotas_used_positive CHECK (used > 0),
    PRIMARY KEY (user_id, quota, day)
);

CREATE INDEX idx_user_daily_quotas_day ON user_daily_quotas (day);
