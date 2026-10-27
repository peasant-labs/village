-- Daily limits on invitations and emails (user_daily_quotas). A day is the
-- UTC calendar day, whatever the session's time zone.

-- name: TakeDailyQuota :one
-- Takes one slot of a daily limit. It returns the count after taking it, or no
-- row when the limit is already reached, in which case nothing is counted. The
-- conditional upsert locks the row, so two requests cannot both take the last
-- slot.
INSERT INTO user_daily_quotas (user_id, quota, day, used)
VALUES (@user_id, @quota, (now() AT TIME ZONE 'UTC')::date, 1)
ON CONFLICT (user_id, quota, day) DO UPDATE
SET used = user_daily_quotas.used + 1
WHERE user_daily_quotas.used < @daily_limit::int
RETURNING used;

-- name: DeleteStaleDailyQuotas :exec
-- Counts older than yesterday no longer limit anything.
DELETE FROM user_daily_quotas
WHERE day < (now() AT TIME ZONE 'UTC')::date - 1;
