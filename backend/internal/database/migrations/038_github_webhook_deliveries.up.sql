-- Idempotency ledger for GitHub App webhook deliveries.
--
-- GitHub redelivers an event whenever a response is not 2xx, and an operator can
-- redeliver any past delivery by hand. Every verified delivery's
-- `X-GitHub-Delivery` value is recorded here before it is dispatched, so a
-- replay is a no-op instead of being handled twice. The id is opaque and unique
-- per delivery; `received_at` is when this server first accepted it.
CREATE TABLE github_webhook_deliveries (
    delivery_id TEXT PRIMARY KEY,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
