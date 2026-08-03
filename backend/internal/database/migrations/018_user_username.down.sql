DROP INDEX IF EXISTS idx_users_github_username_lower;
ALTER TABLE users DROP COLUMN IF EXISTS provider_username;
ALTER TABLE users DROP COLUMN IF EXISTS username_chosen;
