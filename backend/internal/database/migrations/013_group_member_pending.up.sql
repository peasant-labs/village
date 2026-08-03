-- Widen group_members.role to allow 'pending' for users who have requested
-- to join a collective but have not yet been approved by an owner.
ALTER TABLE group_members DROP CONSTRAINT IF EXISTS group_members_role_check;
ALTER TABLE group_members ADD CONSTRAINT group_members_role_check
    CHECK (role IN ('owner', 'member', 'contributor', 'pending'));
