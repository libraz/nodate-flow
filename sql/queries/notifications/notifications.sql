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

-- name: ListNotificationsForUserKeyset :many
-- Keyset-paginated variant of ListNotificationsForUser.
--
-- Cursor encoding: pass the (created_at, public_id) tuple from the LAST
-- row of the previous page. First page passes NULL for both
-- cursor_created_at and cursor_public_id. ORDER BY (created_at DESC,
-- public_id DESC) — the OFFSET variant orders by id DESC as the
-- tiebreaker, but id is internal-only so we use public_id DESC for
-- the keyset cursor (UUID v7 is monotonic so still produces a stable
-- newest-first order).
--
-- read_filter: pass 'all' to include both read and unread, 'unread' for
-- only unread (read_at IS NULL), 'read' for only read (read_at IS NOT NULL).
--
-- Index used: idx_notifications_user_id_keyset
-- (recipient_user_id, created_at, public_id).
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
  n.created_at
FROM notifications n
INNER JOIN workspaces w ON w.id = n.workspace_id
LEFT JOIN users au ON au.id = n.actor_user_id
WHERE n.recipient_user_id = ?
  AND n.archived_at IS NULL
  AND n.enabled = TRUE
  AND (sqlc.arg(read_filter) = 'all'
       OR (sqlc.arg(read_filter) = 'unread' AND n.read_at IS NULL)
       OR (sqlc.arg(read_filter) = 'read'   AND n.read_at IS NOT NULL))
  AND (sqlc.narg(cursor_created_at) IS NULL
       OR n.created_at < sqlc.narg(cursor_created_at)
       OR (n.created_at = sqlc.narg(cursor_created_at)
           AND n.public_id < sqlc.narg(cursor_public_id)))
ORDER BY n.created_at DESC, n.public_id DESC
LIMIT ?;

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

-- name: ListNotificationsForWorkspaceKeyset :many
-- Keyset-paginated variant of ListNotificationsForWorkspace.
--
-- Cursor encoding: pass the (created_at, public_id) tuple from the LAST
-- row of the previous page. First page passes NULL for both
-- cursor_created_at and cursor_public_id. ORDER BY (created_at DESC,
-- public_id DESC).
--
-- read_filter: pass 'all' to include both read and unread, 'unread' for
-- only unread (read_at IS NULL), 'read' for only read (read_at IS NOT NULL).
--
-- Uses the existing idx_notifications_workspace_id_recipient_read index
-- (workspace_id, recipient_user_id, read_at, created_at DESC).
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
  n.created_at
FROM notifications n
INNER JOIN workspaces w ON w.id = n.workspace_id
LEFT JOIN users au ON au.id = n.actor_user_id
WHERE n.workspace_id = ?
  AND n.recipient_user_id = ?
  AND n.archived_at IS NULL
  AND n.enabled = TRUE
  AND (sqlc.arg(read_filter) = 'all'
       OR (sqlc.arg(read_filter) = 'unread' AND n.read_at IS NULL)
       OR (sqlc.arg(read_filter) = 'read'   AND n.read_at IS NOT NULL))
  AND (sqlc.narg(cursor_created_at) IS NULL
       OR n.created_at < sqlc.narg(cursor_created_at)
       OR (n.created_at = sqlc.narg(cursor_created_at)
           AND n.public_id < sqlc.narg(cursor_public_id)))
ORDER BY n.created_at DESC, n.public_id DESC
LIMIT ?;

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
-- Mark a notification as delivered (email/push sent). Scoped to the
-- recipient so a delivery flag can never be flipped on another user's
-- notification, matching the recipient predicate on the sibling
-- MarkNotificationRead / ArchiveNotification mutations.
UPDATE notifications
SET delivered_at = NOW()
WHERE public_id = ?
  AND recipient_user_id = ?
  AND delivered_at IS NULL;

-- name: GetEnabledChannelsForRecipients :many
-- Resolve, for a set of recipients in one workspace, which delivery
-- channels are enabled for a given event_category. A recipient with
-- no row for the (workspace, category, channel) tuple returns no
-- entry; the caller is expected to apply the default (in_app) when a
-- recipient is absent from the result set.
--
-- Only rows with enabled = TRUE AND is_muted = FALSE are returned —
-- a muted preference behaves identically to a disabled one for the
-- purposes of fan-out, and neither should produce a notifications row.
SELECT
  user_id,
  channel
FROM notification_preferences
WHERE workspace_id = ?
  AND event_category = ?
  AND user_id IN (sqlc.slice('user_ids'))
  AND is_muted = FALSE
  AND enabled = TRUE;

-- name: ClaimReminderOccurrence :execrows
-- Claim one occurrence of a calendar event's reminder for delivery.
--
-- The claim is an INSERT, and the unique key on (event_id,
-- occurrence_start) decides the winner: exactly one caller inserts the
-- row and every racing tick or replica gets zero affected rows from the
-- IGNORE. Callers MUST claim first and dispatch only on a count of 1.
--
-- Per occurrence rather than per event, because a recurring series has
-- one reminder per week and a column on the event row can only remember
-- one of them. Claiming on calendar_events.notified_at rang the first
-- Monday and nothing after it.
--
-- On a dispatch failure the caller releases the claim with
-- ReleaseReminderOccurrence so the next tick can retake it.
INSERT IGNORE INTO calendar_event_reminders (workspace_id, event_id, occurrence_start)
VALUES (?, ?, ?);

-- name: ReleaseReminderOccurrence :exec
-- Give back a claim whose dispatch failed, so the next tick retries.
--
-- A real DELETE rather than a flag: the unique key is what stops a
-- second send, so a released claim has to stop occupying it. A
-- tombstone would make the retry impossible in exactly the situation
-- the release exists for.
DELETE FROM calendar_event_reminders
WHERE event_id = ?
  AND occurrence_start = ?;
