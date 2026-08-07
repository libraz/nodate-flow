-- name: CreateCalendar :execlastid
-- Insert a new calendar. kind determines behavior: personal (user-owned
-- layer, may own many) or system (holiday feeds).
INSERT INTO calendars (
  public_id,
  workspace_id,
  kind,
  name,
  description,
  color,
  cover_url,
  owner_user_id,
  system_slug
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindCalendarByPublicId :one
-- Resolve a calendar by UUID v7 within a workspace.
SELECT
  id,
  public_id,
  workspace_id,
  kind,
  default_event_visibility,
  name,
  description,
  color,
  cover_url,
  owner_user_id,
  system_slug,
  enabled,
  updated_at,
  created_at
FROM calendars
WHERE public_id = ?
  AND workspace_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: ListCalendarsForUser :many
-- List the calendars a user may reach within a workspace.
--
-- Driven from calendar_members, because that is what grants access. The
-- subscription is a LEFT JOIN: display preferences are optional, and a
-- member who never touched their sidebar still has to see the calendar.
-- Defaults come from the membership so an absent subscription renders the
-- same as a default one.
SELECT
  c.id,
  c.public_id,
  c.kind,
  c.name,
  c.description,
  c.color,
  c.cover_url,
  c.owner_user_id,
  c.system_slug,
  cm.role,
  cm.member_color,
  COALESCE(cs.display_color, cm.member_color) AS display_color,
  COALESCE(cs.visible, TRUE)                  AS visible,
  COALESCE(cs.sort_weight, cm.sort_weight)    AS subscription_sort_weight,
  c.updated_at,
  c.created_at
FROM calendar_members cm
INNER JOIN calendars c
  ON c.id = cm.calendar_id AND c.enabled = TRUE
LEFT JOIN calendar_subscriptions cs
  ON cs.calendar_id = cm.calendar_id
 AND cs.user_id = cm.user_id
 AND cs.enabled = TRUE
WHERE cm.user_id = ?
  AND cm.workspace_id = ?
  AND cm.enabled = TRUE
ORDER BY COALESCE(cs.sort_weight, cm.sort_weight) ASC, c.created_at ASC;

-- name: PatchCalendar :exec
-- Patch mutable calendar fields. NULL params leave columns untouched.
UPDATE calendars
SET name        = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    color       = COALESCE(sqlc.narg('color'), color),
    cover_url   = COALESCE(sqlc.narg('cover_url'), cover_url)
WHERE public_id = ?
  AND workspace_id = ?
  AND enabled = TRUE;

-- name: DisableCalendar :exec
-- Soft-delete a calendar.
UPDATE calendars
SET enabled = FALSE
WHERE public_id = ?
  AND workspace_id = ?;

-- name: FindPersonalCalendar :one
-- Find the personal calendar for a user in a workspace.
SELECT
  id,
  public_id,
  kind,
  name,
  color,
  owner_user_id,
  enabled,
  created_at
FROM calendars
WHERE workspace_id = ?
  AND owner_user_id = ?
  AND kind = 'personal'
  AND enabled = TRUE
LIMIT 1;

-- name: ListDiscoverableCalendarsInWorkspace :many
-- List teammate personal calendars in a workspace that the actor is not
-- currently subscribed to. Used by the "Add teammate calendar" picker in
-- the right-rail Calendars panel. Excludes:
--   - calendars owned by the actor (their own personal calendar)
--   - calendars the actor already holds a calendar_members grant on
--     for the actor
--   - non-personal calendars (system/holiday/etc — those have their own UI)
-- Owners must still be active workspace members so we don't surface
-- calendars whose owner has left the workspace.
SELECT
  c.id,
  c.public_id,
  c.kind,
  c.name,
  c.description,
  c.color,
  c.cover_url,
  c.owner_user_id,
  uo.public_id AS owner_public_id,
  uo.display_name AS owner_display_name,
  uo.avatar_url AS owner_avatar_url,
  c.system_slug,
  c.updated_at,
  c.created_at
FROM calendars c
INNER JOIN users uo
  ON uo.id = c.owner_user_id AND uo.enabled = TRUE
INNER JOIN workspace_members wm
  ON wm.workspace_id = c.workspace_id
  AND wm.user_id = c.owner_user_id
  AND wm.enabled = TRUE
WHERE c.workspace_id = ?
  AND c.kind = 'personal'
  AND c.enabled = TRUE
  AND c.owner_user_id <> CAST(sqlc.arg('actor_id') AS UNSIGNED)
  AND NOT EXISTS (
    SELECT 1 FROM calendar_members cm
    WHERE cm.calendar_id = c.id
      AND cm.user_id = CAST(sqlc.arg('actor_id') AS UNSIGNED)
      AND cm.enabled = TRUE
  )
ORDER BY uo.display_name ASC, c.created_at ASC
LIMIT 200;
