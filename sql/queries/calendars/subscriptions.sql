-- name: CreateCalendarSubscription :execlastid
-- Subscribe a user to a calendar with display preferences.
-- Not an ACL axis — event-level visibility governs access.
INSERT INTO calendar_subscriptions (
  public_id,
  workspace_id,
  calendar_id,
  user_id,
  display_color
) VALUES (?, ?, ?, ?, ?);

-- name: FindCalendarSubscription :one
-- Look up a user's subscription to a specific calendar.
SELECT
  id,
  public_id,
  calendar_id,
  user_id,
  display_color,
  visible,
  sort_weight,
  enabled,
  created_at
FROM calendar_subscriptions
WHERE calendar_id = ?
  AND user_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: ListCalendarSubscribers :many
-- List all subscribers of a calendar (for member list, color resolution).
SELECT
  cs.public_id,
  cs.user_id,
  cs.visible,
  cs.sort_weight,
  u.public_id AS user_public_id,
  u.display_name,
  u.avatar_url,
  cs.created_at
FROM calendar_subscriptions cs
INNER JOIN users u ON u.id = cs.user_id AND u.enabled = TRUE
WHERE cs.calendar_id = ?
  AND cs.workspace_id = ?
  AND cs.enabled = TRUE
ORDER BY cs.sort_weight ASC, cs.created_at ASC;

-- name: PatchCalendarSubscription :exec
-- Update a subscriber's display preferences.
UPDATE calendar_subscriptions
SET display_color = COALESCE(sqlc.narg('display_color'), display_color),
    visible       = COALESCE(sqlc.narg('visible'), visible),
    sort_weight   = COALESCE(sqlc.narg('sort_weight'), sort_weight)
WHERE calendar_id = ?
  AND user_id = ?
  AND enabled = TRUE;

-- name: DisableCalendarSubscription :exec
-- Remove a user from a calendar (soft-delete).
UPDATE calendar_subscriptions
SET enabled = FALSE
WHERE calendar_id = ?
  AND user_id = ?;
