-- Per-collective toggle controlling whether the public Members card is shown
-- on the collective detail page. Owners/admins still see the card regardless.
-- Default ON so existing collectives keep their current behavior.
ALTER TABLE groups
    ADD COLUMN display_members BOOLEAN NOT NULL DEFAULT TRUE;
