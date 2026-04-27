-- name: CreateWebhookSubscription :execlastid
INSERT INTO webhook_subscriptions (public_id, workspace_id, creator_id, url, secret, description, event_types)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListWebhookSubscriptions :many
-- List webhook subscriptions for a workspace with pagination.
SELECT
  ws.public_id, ws.workspace_id, ws.url, ws.description,
  ws.event_types, ws.is_active,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  ws.updated_at, ws.created_at,
  COUNT(*) OVER() AS total
FROM webhook_subscriptions ws
INNER JOIN users u ON u.id = ws.creator_id
WHERE ws.workspace_id = ? AND ws.enabled = TRUE
ORDER BY ws.created_at DESC
LIMIT ? OFFSET ?;

-- name: GetWebhookSubscription :one
SELECT ws.public_id, ws.workspace_id, ws.url, ws.secret, ws.description,
  ws.event_types, ws.is_active,
  ws.updated_at, ws.created_at
FROM webhook_subscriptions ws
WHERE ws.workspace_id = ? AND ws.public_id = ? AND ws.enabled = TRUE;

-- name: DeleteWebhookSubscription :exec
UPDATE webhook_subscriptions SET enabled = FALSE
WHERE workspace_id = ? AND public_id = ?;

-- name: ToggleWebhookSubscription :exec
UPDATE webhook_subscriptions SET is_active = ?
WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE;

-- name: ListActiveSubscriptionsForEvent :many
-- Find all active subscriptions in a workspace. Event type filtering
-- is done in Go since JSON_CONTAINS is not sqlc-friendly.
-- id is required: used as subscription_id FK in CreateWebhookDelivery.
SELECT id, public_id, url, secret, event_types
FROM webhook_subscriptions
WHERE workspace_id = ? AND is_active = TRUE AND enabled = TRUE;

-- name: CreateWebhookDelivery :execrows
-- Insert a delivery row, deduping against the (subscription_id, event_public_id)
-- unique key so the same event fanned out twice (e.g. eventbus retry) does
-- not enqueue two HTTP attempts for the same subscription.
-- Returns the affected-row count (1 = inserted, 0 = duplicate ignored) so
-- the worker can branch on the dedupe outcome without a follow-up SELECT.
INSERT IGNORE INTO webhook_deliveries (
  public_id, workspace_id, subscription_id, event_type,
  event_public_id, payload_json, status, next_retry_at
) VALUES (?, ?, ?, ?, ?, ?, 'pending', ?);

-- name: ListWebhookDeliveries :many
-- List deliveries for a subscription with pagination.
SELECT
  d.public_id, d.event_type, d.status,
  d.http_status, d.attempts, d.max_attempts,
  d.delivered_at, d.failed_at, d.created_at,
  COUNT(*) OVER() AS total
FROM webhook_deliveries d
WHERE d.workspace_id = ? AND d.subscription_id = (
  SELECT wsub.id FROM webhook_subscriptions wsub WHERE wsub.workspace_id = ? AND wsub.public_id = ? AND wsub.enabled = TRUE LIMIT 1
)
ORDER BY d.created_at DESC
LIMIT ? OFFSET ?;

-- name: FindPendingDeliveries :many
-- Find deliveries ready for (re)delivery. Used by the background worker.
-- d.id is required: used by MarkDeliveryDelivered/Failed/Dead (WHERE id = ?).
SELECT d.id, d.public_id, d.workspace_id, d.subscription_id,
  d.event_type, d.payload_json, d.attempts, d.max_attempts,
  ws.url, ws.secret
FROM webhook_deliveries d
INNER JOIN webhook_subscriptions ws ON ws.id = d.subscription_id
WHERE d.status IN ('pending', 'failed')
  AND d.next_retry_at <= NOW()
  AND d.attempts < d.max_attempts
  AND d.enabled = TRUE
ORDER BY d.next_retry_at ASC
LIMIT ?;

-- name: MarkDeliveryDelivered :exec
UPDATE webhook_deliveries
SET status = 'delivered', http_status = ?, response_body = ?,
    attempts = attempts + 1, delivered_at = NOW(), next_retry_at = NULL
WHERE id = ?;

-- name: MarkDeliveryFailed :exec
-- Mark a delivery attempt as failed with retry scheduling.
UPDATE webhook_deliveries
SET status = 'failed', http_status = ?, response_body = ?,
    attempts = attempts + 1, next_retry_at = ?
WHERE id = ?;

-- name: MarkDeliveryDead :exec
-- Mark a delivery as dead (all retries exhausted).
UPDATE webhook_deliveries
SET status = 'dead', http_status = ?, response_body = ?,
    attempts = attempts + 1, failed_at = NOW(), next_retry_at = NULL
WHERE id = ?;
