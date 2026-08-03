-- Link a collective (group) to a GitHub organization.
-- The link is opt-in metadata only: it does NOT auto-create memberships.
-- It enables:
--   - discovery on the org's profile page,
--   - search filtering by org login, and
--   - a precise "verified_only" gate that requires THIS specific org
--     instead of "any visible org".
ALTER TABLE groups ADD COLUMN linked_github_org VARCHAR(255);

-- Case-insensitive lookup index for join by org login.
CREATE INDEX idx_groups_linked_github_org_lower ON groups (lower(linked_github_org));
