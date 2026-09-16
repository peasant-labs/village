ALTER TABLE github_webhook_deliveries
    DROP CONSTRAINT IF EXISTS github_webhook_deliveries_status_menu,
    DROP CONSTRAINT IF EXISTS github_webhook_deliveries_attempts_nonnegative,
    DROP COLUMN IF EXISTS event_type,
    DROP COLUMN IF EXISTS payload,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS attempts,
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS handled_at,
    DROP COLUMN IF EXISTS failed_at;
