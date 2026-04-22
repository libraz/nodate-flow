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
-- List all calendars a user subscribes to within a workspace.
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
  cs.display_color,
  cs.visible,
  cs.sort_weight AS subscription_sort_weight,
  c.updated_at,
  c.created_at
FROM calendar_subscriptions cs
INNER JOIN calendars c
  ON c.id = cs.calendar_id AND c.enabled = TRUE
WHERE cs.user_id = ?
  AND cs.workspace_id = ?
  AND cs.enabled = TRUE
ORDER BY cs.sort_weight ASC, c.created_at ASC;

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

-- name: FindSystemCalendarBySlug :one
-- Find a system calendar by its slug within a workspace.
SELECT
  id,
  public_id,
  kind,
  name,
  system_slug,
  enabled,
  created_at
FROM calendars
WHERE workspace_id = ?
  AND system_slug = ?
  AND enabled = TRUE
LIMIT 1;
