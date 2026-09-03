-- name: CreateCalendarSubscription :execlastid
-- Subscribe a user to a calendar with display preferences.
-- Not an ACL axis — event-level visibility governs access.
--
-- uniq_calendar_subscriptions_calendar_user covers unsubscribed rows, so
-- the row survives an unsubscribe holding the (calendar, user) pair and
-- a second subscribe has to revive it. Re-subscribing installs the
-- colour asked for now and adopts the caller's public_id, the same
-- reading PatchCalendarSubscription takes of a returning subscriber.
INSERT INTO calendar_subscriptions (
  public_id,
  workspace_id,
  calendar_id,
  user_id,
  display_color
) VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  public_id     = VALUES(public_id),
  display_color = VALUES(display_color),
  enabled       = TRUE;

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

-- name: PatchCalendarSubscription :execrows
-- Create or update one user's private display preferences for a calendar.
--
-- An upsert rather than an UPDATE because nothing else writes this table.
-- Membership lives in calendar_members, so the first time someone picks a
-- colour or hides a layer there is no row to update yet: as a plain UPDATE
-- this matched nothing, and the API reported a preference that was never
-- stored, which the user saw as a colour that reverted on the next read.
--
-- The identity parameters and the new_* values are used on the insert path
-- only. new_* should carry the caller's requested value where the request
-- supplies one and the column default otherwise -- a row being created has
-- no earlier preference to preserve. The nullable display_color / visible /
-- sort_weight parameters drive the duplicate-key branch, where NULL keeps
-- the stored value so a field the request omits stays untouched.
--
-- public_id is written on the insert path only. A row that already exists
-- keeps the identity it has published; recolouring a layer must not rotate
-- an ID that has been on the wire.
--
-- enabled is reset to TRUE because uniq_calendar_subscriptions_calendar_user
-- covers disabled rows too: without the reset the INSERT collides, the
-- update leaves enabled = FALSE, and every reader keeps skipping the row --
-- FindCalendarSubscription filters on it -- while the API reports the value
-- the caller just wrote. Reviving grants nothing, since access is
-- calendar_members alone, so someone who dropped a layer and later
-- recolours it simply gets their own preferences back.
--
-- The duplicate-key branch is keyed on (calendar_id, user_id) rather than
-- carrying an explicit workspace predicate: a calendar belongs to exactly
-- one workspace, and the caller resolves it inside the workspace before
-- writing.
--
-- updated_at is assigned explicitly so the update branch always counts as a
-- change. RowsAffected is then 1 for a created row and 2 for an updated one
-- (MySQL counts a duplicate-key update as two). Callers MUST inspect it,
-- but 0 here is an identical re-write rather than a missing row -- there is
-- no predicate that can fail -- and MUST NOT be reported as not-found.
INSERT INTO calendar_subscriptions (
  public_id,
  workspace_id,
  calendar_id,
  user_id,
  display_color,
  visible,
  sort_weight
) VALUES (
  sqlc.arg('public_id'),
  sqlc.arg('workspace_id'),
  sqlc.arg('calendar_id'),
  sqlc.arg('user_id'),
  sqlc.arg('new_display_color'),
  sqlc.arg('new_visible'),
  sqlc.arg('new_sort_weight')
)
ON DUPLICATE KEY UPDATE
  display_color = COALESCE(sqlc.narg('display_color'), display_color),
  visible       = COALESCE(sqlc.narg('visible'), visible),
  sort_weight   = COALESCE(sqlc.narg('sort_weight'), sort_weight),
  enabled       = TRUE,
  updated_at    = NOW(3);

-- name: DisableCalendarSubscription :exec
-- Remove a user from a calendar (soft-delete).
--
-- affected-rows: not-applicable — the (calendar_id, user_id) pair is a
-- membership, and unsubscribing a user who is not subscribed asks for the
-- state that already holds. Membership itself is answered by the calendar
-- member path, which resolves the row with FindCalendarMember first.
UPDATE calendar_subscriptions
SET enabled = FALSE
WHERE calendar_id = ?
  AND user_id = ?
  AND enabled = TRUE;
