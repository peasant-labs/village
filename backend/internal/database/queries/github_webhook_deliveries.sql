-- name: RecordGitHubWebhookDelivery :one
-- Records a delivery together with the type and raw payload an attempt needs to
-- be resumed, and returns the ledger's current state for it in one round trip.
-- A new delivery starts pending; an existing row keeps its payload and status,
-- so the receiver can tell a first or resumable attempt (pending, failed) from
-- an already-handled replay (handled) without a second statement.
INSERT INTO github_webhook_deliveries (delivery_id, event_type, payload, status)
VALUES (@delivery_id, @event_type, @payload, 'pending')
ON CONFLICT (delivery_id) DO UPDATE SET delivery_id = EXCLUDED.delivery_id
RETURNING status, attempts;

-- name: CompleteGitHubWebhookDelivery :exec
-- Records one attempt's outcome and counts the attempt. A handled delivery is
-- never dispatched again; a failed one is a candidate for the next redelivery.
-- The caller bounds last_error before passing it, since it is stored verbatim
-- for an operator rather than shown to a user.
UPDATE github_webhook_deliveries SET
    status = @status,
    attempts = attempts + 1,
    last_error = @last_error,
    handled_at = CASE WHEN @status = 'handled' THEN now() ELSE handled_at END,
    failed_at = CASE WHEN @status = 'failed' THEN now() ELSE failed_at END
WHERE delivery_id = @delivery_id;
