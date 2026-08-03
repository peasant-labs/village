-- Widen group_members.role to allow 'contributor'
ALTER TABLE group_members DROP CONSTRAINT IF EXISTS group_members_role_check;
ALTER TABLE group_members ADD CONSTRAINT group_members_role_check
    CHECK (role IN ('owner', 'member', 'contributor'));

-- Add data_access policy to groups
ALTER TABLE groups ADD COLUMN IF NOT EXISTS data_access VARCHAR(20) NOT NULL DEFAULT 'members_only';
