-- name: CreateCalendarEvent :execlastid
-- Insert a new calendar event.
INSERT INTO calendar_events (
  public_id,
  workspace_id,
  calendar_id,
  kind,
  visibility,
  show_as,
  title,
  all_day,
  start_at,
  end_at,
  timezone,
  location,
  memo,
  url,
  owner_user_id,
  created_by_user_id,
  block_label,
  recurrence_rule,
  recurrence_end,
  recurrence_exceptions,
  notification_offset,
  task_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindCalendarEventByPublicId :one
-- Resolve a calendar event by UUID v7 within a calendar.
SELECT
  id,
  public_id,
  workspace_id,
  calendar_id,
  kind,
  visibility,
  show_as,
  title,
  all_day,
  start_at,
  end_at,
  timezone,
  location,
  memo,
  url,
  owner_user_id,
  created_by_user_id,
  block_label,
  COALESCE(recurrence_rule, CAST('null' AS JSON)) AS recurrence_rule,
  recurrence_end,
  COALESCE(recurrence_exceptions, CAST('null' AS JSON)) AS recurrence_exceptions,
  notification_offset,
  task_id,
  enabled,
  updated_at,
  created_at
FROM calendar_events
WHERE public_id = ?
  AND calendar_id = ?
  AND workspace_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: ListCalendarEventsByRange :many
-- List non-recurring events in a calendar within a time range.
SELECT
  ce.public_id,
  ce.kind,
  ce.visibility,
  ce.show_as,
  ce.title,
  ce.all_day,
  ce.start_at,
  ce.end_at,
  ce.timezone,
  ce.location,
  ce.memo,
  ce.url,
  ce.owner_user_id,
  ce.created_by_user_id,
  ce.block_label,
  ce.notification_offset,
  ce.task_id,
  ce.updated_at,
  ce.created_at
FROM calendar_events ce
WHERE ce.calendar_id = ?
  AND ce.recurrence_rule IS NULL
  AND ce.start_at < ?
  AND ce.end_at > ?
  AND ce.enabled = TRUE
ORDER BY ce.start_at ASC, ce.public_id ASC
LIMIT 1000;

-- name: ListRecurringCalendarEventsByRange :many
-- List recurring events whose recurrence window overlaps the query range.
SELECT
  ce.public_id,
  ce.kind,
  ce.visibility,
  ce.show_as,
  ce.title,
  ce.all_day,
  ce.start_at,
  ce.end_at,
  ce.timezone,
  ce.location,
  ce.memo,
  ce.url,
  ce.owner_user_id,
  ce.created_by_user_id,
  ce.block_label,
  ce.recurrence_rule,
  ce.recurrence_end,
  ce.recurrence_exceptions,
  ce.notification_offset,
  ce.task_id,
  ce.updated_at,
  ce.created_at
FROM calendar_events ce
WHERE ce.calendar_id = ?
  AND ce.recurrence_rule IS NOT NULL
  AND ce.start_at < ?
  AND (ce.recurrence_end IS NULL OR ce.recurrence_end > ?)
  AND ce.enabled = TRUE
ORDER BY ce.start_at ASC
LIMIT 1000;

-- name: ListCalendarEventsAcrossCalendars :many
-- Cross-calendar query: list events across multiple calendars for a user
-- within a workspace and time range. Used by the unified calendar view.
SELECT
  ce.public_id,
  ce.calendar_id,
  c.public_id AS calendar_public_id,
  ce.kind,
  ce.visibility,
  ce.show_as,
  ce.title,
  ce.all_day,
  ce.start_at,
  ce.end_at,
  ce.timezone,
  ce.location,
  ce.owner_user_id,
  ce.block_label,
  ce.task_id,
  ce.updated_at,
  ce.created_at
FROM calendar_events ce
INNER JOIN calendars c
  ON c.id = ce.calendar_id
INNER JOIN calendar_subscriptions cs
  ON cs.calendar_id = ce.calendar_id
  AND cs.user_id = ?
  AND cs.visible = TRUE
  AND cs.enabled = TRUE
WHERE ce.workspace_id = ?
  AND ce.recurrence_rule IS NULL
  AND ce.start_at < ?
  AND ce.end_at > ?
  AND ce.enabled = TRUE
ORDER BY ce.start_at ASC, ce.public_id ASC
LIMIT 1000;

-- name: ListRecurringCalendarEventsAcrossCalendars :many
-- Cross-calendar query: list recurring events across multiple calendars
-- whose recurrence window overlaps the query range.
SELECT
  ce.public_id,
  ce.calendar_id,
  c.public_id AS calendar_public_id,
  ce.kind,
  ce.visibility,
  ce.show_as,
  ce.title,
  ce.all_day,
  ce.start_at,
  ce.end_at,
  ce.timezone,
  ce.location,
  ce.owner_user_id,
  ce.block_label,
  ce.recurrence_rule,
  ce.recurrence_end,
  ce.recurrence_exceptions,
  ce.task_id,
  ce.updated_at,
  ce.created_at
FROM calendar_events ce
INNER JOIN calendars c
  ON c.id = ce.calendar_id
INNER JOIN calendar_subscriptions cs
  ON cs.calendar_id = ce.calendar_id
  AND cs.user_id = ?
  AND cs.visible = TRUE
  AND cs.enabled = TRUE
WHERE ce.workspace_id = ?
  AND ce.recurrence_rule IS NOT NULL
  AND ce.start_at < ?
  AND (ce.recurrence_end IS NULL OR ce.recurrence_end > ?)
  AND ce.enabled = TRUE
ORDER BY ce.start_at ASC
LIMIT 1000;

-- name: PatchCalendarEvent :exec
-- Patch mutable event fields. NULL params leave columns untouched.
UPDATE calendar_events
SET kind                = COALESCE(sqlc.narg('kind'), kind),
    visibility          = COALESCE(sqlc.narg('visibility'), visibility),
    show_as             = COALESCE(sqlc.narg('show_as'), show_as),
    title               = COALESCE(sqlc.narg('title'), title),
    all_day             = COALESCE(sqlc.narg('all_day'), all_day),
    start_at            = COALESCE(sqlc.narg('start_at'), start_at),
    end_at              = COALESCE(sqlc.narg('end_at'), end_at),
    timezone            = COALESCE(sqlc.narg('timezone'), timezone),
    location            = COALESCE(sqlc.narg('location'), location),
    memo                = COALESCE(sqlc.narg('memo'), memo),
    url                 = COALESCE(sqlc.narg('url'), url),
    owner_user_id       = COALESCE(sqlc.narg('owner_user_id'), owner_user_id),
    block_label         = COALESCE(sqlc.narg('block_label'), block_label),
    recurrence_rule       = COALESCE(sqlc.narg('recurrence_rule'), recurrence_rule),
    recurrence_end        = COALESCE(sqlc.narg('recurrence_end'), recurrence_end),
    recurrence_exceptions = COALESCE(sqlc.narg('recurrence_exceptions'), recurrence_exceptions),
    notification_offset   = COALESCE(sqlc.narg('notification_offset'), notification_offset),
    task_id             = COALESCE(sqlc.narg('task_id'), task_id)
WHERE public_id = ?
  AND calendar_id = ?
  AND workspace_id = ?
  AND enabled = TRUE;

-- name: DisableCalendarEvent :exec
-- Soft-delete a calendar event.
UPDATE calendar_events
SET enabled = FALSE
WHERE public_id = ?
  AND calendar_id = ?
  AND workspace_id = ?;

-- name: ListAllCalendarEvents :many
-- List all enabled events in a calendar (no date filter, for export).
SELECT
  ce.public_id,
  ce.kind,
  ce.visibility,
  ce.show_as,
  ce.title,
  ce.all_day,
  ce.start_at,
  ce.end_at,
  ce.timezone,
  ce.location,
  ce.memo,
  ce.url,
  ce.block_label,
  ce.recurrence_rule,
  ce.recurrence_end,
  ce.recurrence_exceptions,
  ce.notification_offset,
  ce.updated_at,
  ce.created_at
FROM calendar_events ce
WHERE ce.calendar_id = ?
  AND ce.enabled = TRUE
ORDER BY ce.start_at ASC, ce.public_id ASC
LIMIT 10000;

-- name: FindCalendarEventOwner :one
-- Quick lookup for permission checks: who owns this event?
SELECT owner_user_id, calendar_id, workspace_id
FROM calendar_events
WHERE public_id = ?
  AND workspace_id = ?
  AND enabled = TRUE
LIMIT 1;
