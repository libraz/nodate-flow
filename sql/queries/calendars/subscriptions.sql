-- name: CreateCalendarSubscription :execlastid
-- Subscribe a user to a calendar with a role and display preferences.
INSERT INTO calendar_subscriptions (
  public_id,
  workspace_id,
  calendar_id,
  user_id,
  role,
  member_color,
  display_color
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: FindCalendarSubscription :one
-- Look up a user's subscription to a specific calendar.
SELECT
  id,
  public_id,
  calendar_id,
  user_id,
  role,
  member_color,
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
  cs.role,
  cs.member_color,
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
-- Update a subscriber's role or display preferences.
UPDATE calendar_subscriptions
SET role          = COALESCE(sqlc.narg('role'), role),
    member_color  = COALESCE(sqlc.narg('member_color'), member_color),
    display_color = COALESCE(sqlc.narg('display_color'), display_color),
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

-- name: CountCalendarOwners :one
-- Count how many owners a calendar has (prevent last-owner removal).
SELECT COUNT(*) AS owner_count
FROM calendar_subscriptions
WHERE calendar_id = ?
  AND role = 'owner'
  AND enabled = TRUE;

-- name: CreateMemberFilter :exec
-- Hide a specific member's events within a shared calendar for a subscriber.
INSERT INTO calendar_member_filters (subscription_id, target_user_id)
VALUES (?, ?)
ON DUPLICATE KEY UPDATE enabled = TRUE;

-- name: RemoveMemberFilter :exec
-- Unhide a specific member's events.
UPDATE calendar_member_filters
SET enabled = FALSE
WHERE subscription_id = ?
  AND target_user_id = ?;

-- name: ListMemberFilters :many
-- List hidden members for a subscription.
SELECT target_user_id
FROM calendar_member_filters
WHERE subscription_id = ?
  AND enabled = TRUE;
