-- name: CreateNotification :execrows
-- Create a single notification entry for a recipient. INSERT IGNORE skips
-- rows that collide with the (recipient_user_id, source_event_id, channel)
-- unique key, providing at-least-once / idempotent fan-out semantics. The
-- caller can read the affected-rows count to distinguish a fresh insert
-- (1) from a deduplicated retry (0).
INSERT IGNORE INTO notifications (
  public_id,
  workspace_id,
  recipient_user_id,
  actor_user_id,
  source_event_id,
  event_type,
  resource_type,
  resource_public_id,
  title,
  body,
  severity,
  channel
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListNotificationsForUser :many
-- List notifications for a user across all their workspaces, ordered newest first.
-- Excludes archived and disabled notifications.
SELECT
  n.public_id,
  n.workspace_id,
  w.public_id AS workspace_public_id,
  n.actor_user_id,
  au.public_id AS actor_public_id,
  au.display_name AS actor_display_name,
  n.event_type,
  n.resource_type,
  n.resource_public_id,
  n.title,
  n.body,
  n.severity,
  n.channel,
  n.read_at,
  n.delivered_at,
  n.created_at,
  COUNT(*) OVER() AS total
FROM notifications n
INNER JOIN workspaces w ON w.id = n.workspace_id
LEFT JOIN users au ON au.id = n.actor_user_id
WHERE n.recipient_user_id = ?
  AND n.archived_at IS NULL
  AND n.enabled = TRUE
ORDER BY n.created_at DESC, n.id DESC
LIMIT ? OFFSET ?;

-- name: ListNotificationsForWorkspace :many
-- List notifications for a user within a specific workspace.
SELECT
  n.public_id,
  n.workspace_id,
  w.public_id AS workspace_public_id,
  n.actor_user_id,
  au.public_id AS actor_public_id,
  au.display_name AS actor_display_name,
  n.event_type,
  n.resource_type,
  n.resource_public_id,
  n.title,
  n.body,
  n.severity,
  n.channel,
  n.read_at,
  n.delivered_at,
  n.created_at,
  COUNT(*) OVER() AS total
FROM notifications n
INNER JOIN workspaces w ON w.id = n.workspace_id
LEFT JOIN users au ON au.id = n.actor_user_id
WHERE n.workspace_id = ?
  AND n.recipient_user_id = ?
  AND n.archived_at IS NULL
  AND n.enabled = TRUE
ORDER BY n.created_at DESC, n.id DESC
LIMIT ? OFFSET ?;

-- name: CountUnreadNotifications :one
-- Count unread notifications for a user across all workspaces.
-- Used by the global notification badge when no workspace is selected.
SELECT COUNT(*) AS unread_count
FROM notifications
WHERE recipient_user_id = ?
  AND read_at IS NULL
  AND archived_at IS NULL
  AND enabled = TRUE;

-- name: CountUnreadNotificationsForWorkspace :one
-- Count unread notifications for a user within a specific workspace.
SELECT COUNT(*) AS unread_count
FROM notifications
WHERE workspace_id = ?
  AND recipient_user_id = ?
  AND read_at IS NULL
  AND archived_at IS NULL
  AND enabled = TRUE;

-- name: MarkNotificationRead :exec
-- Mark a single notification as read.
UPDATE notifications
SET read_at = NOW()
WHERE public_id = ?
  AND recipient_user_id = ?
  AND read_at IS NULL;

-- name: MarkAllNotificationsRead :exec
-- Mark all unread notifications as read for a user in a workspace.
UPDATE notifications
SET read_at = NOW()
WHERE workspace_id = ?
  AND recipient_user_id = ?
  AND read_at IS NULL
  AND archived_at IS NULL
  AND enabled = TRUE;

-- name: ArchiveNotification :exec
-- Archive a single notification.
UPDATE notifications
SET archived_at = NOW()
WHERE public_id = ?
  AND recipient_user_id = ?
  AND archived_at IS NULL;

-- name: MarkNotificationDelivered :exec
-- Mark a notification as delivered (email/push sent).
UPDATE notifications
SET delivered_at = NOW()
WHERE public_id = ?
  AND delivered_at IS NULL;
