-- name: RecordGitHubWebhookDelivery :execrows
-- Records one webhook delivery id. Returns the number of rows inserted: 1 for a
-- first delivery, 0 when the id was already recorded (a replay), which is how
-- the receiver tells the two apart without a second round trip.
INSERT INTO github_webhook_deliveries (delivery_id)
VALUES ($1)
ON CONFLICT (delivery_id) DO NOTHING;
