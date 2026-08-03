DROP INDEX IF EXISTS idx_groups_linked_github_org_lower;
ALTER TABLE groups DROP COLUMN IF EXISTS linked_github_org;
