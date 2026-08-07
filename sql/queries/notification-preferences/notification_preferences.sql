-- name: UpsertNotificationPreference :exec
-- Create or update a notification preference for a user.
--
-- enabled is reset to TRUE on update because a soft-disabled row still
-- occupies the (user, workspace, category, channel) unique key: without
-- the reset the INSERT collides, the UPDATE leaves enabled = FALSE, and
-- every reader keeps skipping the row while the API reports the value
-- the caller just wrote.
INSERT INTO notification_preferences (
  public_id,
  workspace_id,
  user_id,
  event_category,
  channel,
  is_muted
) VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  is_muted   = VALUES(is_muted),
  enabled    = TRUE,
  updated_at = NOW();

-- name: ListNotificationPreferencesForUser :many
-- List all notification preferences for a user in a workspace.
SELECT
  np.public_id,
  np.event_category,
  np.channel,
  np.is_muted,
  np.updated_at,
  np.created_at
FROM notification_preferences np
WHERE np.workspace_id = ?
  AND np.user_id = ?
  AND np.enabled = TRUE
ORDER BY np.event_category ASC, np.channel ASC;

-- name: FindNotificationPreference :one
-- Check if a specific event category + channel is muted for a user.
SELECT
  np.public_id,
  np.is_muted
FROM notification_preferences np
WHERE np.workspace_id = ?
  AND np.user_id = ?
  AND np.event_category = ?
  AND np.channel = ?
  AND np.enabled = TRUE;

-- name: IsEventMutedForUser :one
-- Quick check: is this event category muted on the in_app channel for a user?
SELECT COUNT(*) AS muted_count
FROM notification_preferences
WHERE workspace_id = ?
  AND user_id = ?
  AND event_category = ?
  AND channel = 'in_app'
  AND is_muted = TRUE
  AND enabled = TRUE;
