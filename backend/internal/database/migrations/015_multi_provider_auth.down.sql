DROP INDEX IF EXISTS idx_users_provider_provider_user_id;

ALTER TABLE users
    DROP COLUMN IF EXISTS provider_user_id,
    DROP COLUMN IF EXISTS provider;
