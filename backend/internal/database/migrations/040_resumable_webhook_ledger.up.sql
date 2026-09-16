-- Make the delivery ledger resumable.
--
-- 038 recorded a delivery id BEFORE dispatch, so a failed handling was
-- permanent: the row existed, so a redelivery was answered as a replay and the
-- event was never dispatched again. GitHub does not redeliver automatically, so
-- a redelivery — by hand or by a script — is the only recovery there is, and the
-- old shape refused it.
--
-- The ledger now carries what is needed to resume an attempt: the event type,
-- the raw payload, a status, an attempt count, and per-status timestamps.
-- Rows written before this migration were dispatched under the old model, where
-- a recorded delivery was never retried, so they are marked handled: they are
-- not candidates for a resumed attempt.
ALTER TABLE github_webhook_deliveries
    ADD COLUMN event_type TEXT        NOT NULL DEFAULT '',
    ADD COLUMN payload    BYTEA       NOT NULL DEFAULT ''::bytea,
    ADD COLUMN status     TEXT        NOT NULL DEFAULT 'pending',
    ADD COLUMN attempts   INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN last_error TEXT,
    ADD COLUMN handled_at TIMESTAMPTZ,
    ADD COLUMN failed_at  TIMESTAMPTZ,
    ADD CONSTRAINT github_webhook_deliveries_status_menu
        CHECK (status IN ('pending', 'handled', 'failed')),
    ADD CONSTRAINT github_webhook_deliveries_attempts_nonnegative
        CHECK (attempts >= 0);

UPDATE github_webhook_deliveries
SET status = 'handled', handled_at = received_at
WHERE status = 'pending';
