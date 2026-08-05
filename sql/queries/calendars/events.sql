-- name: CreateCalendarEvent :execlastid
-- Insert a new calendar event.
INSERT INTO calendar_events (
  public_id,
  workspace_id,
  calendar_id,
  kind,
  visibility,
  show_as,
  flexibility,
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
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindCalendarEventByPublicId :one
-- Resolve a calendar event by UUID v7 within a calendar. The creator
-- (created_by_user_id) is LEFT JOINed so a soft-disabled creator yields
-- NULL identity columns rather than dropping the event row.
SELECT
  ce.id,
  ce.public_id,
  ce.workspace_id,
  ce.calendar_id,
  ce.kind,
  ce.visibility,
  ce.show_as,
  ce.flexibility,
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
  uc.public_id AS creator_public_id,
  uc.display_name AS creator_display_name,
  uc.avatar_url AS creator_avatar_url,
  ce.block_label,
  COALESCE(ce.recurrence_rule, CAST('null' AS JSON)) AS recurrence_rule,
  ce.recurrence_end,
  COALESCE(ce.recurrence_exceptions, CAST('null' AS JSON)) AS recurrence_exceptions,
  ce.notification_offset,
  ce.task_id,
  ce.enabled,
  ce.updated_at,
  ce.created_at
FROM calendar_events ce
LEFT JOIN users uc
  ON uc.id = ce.created_by_user_id
WHERE ce.public_id = ?
  AND ce.calendar_id = ?
  AND ce.workspace_id = ?
  AND ce.enabled = TRUE
LIMIT 1;

-- name: ListCalendarEventsByRange :many
-- List non-recurring events in a calendar within a time range.
SELECT
  ce.public_id,
  ce.kind,
  ce.visibility,
  ce.show_as,
  ce.flexibility,
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
  uc.public_id AS creator_public_id,
  uc.display_name AS creator_display_name,
  uc.avatar_url AS creator_avatar_url,
  ce.block_label,
  ce.notification_offset,
  ce.task_id,
  ce.updated_at,
  ce.created_at,
  EXISTS (
    SELECT 1 FROM calendar_event_attendees a
    WHERE a.event_id = ce.id
      AND a.user_id = sqlc.arg(viewer_user_id)
      AND a.enabled = TRUE
  ) AS is_attendee
FROM calendar_events ce
LEFT JOIN users uc
  ON uc.id = ce.created_by_user_id
WHERE ce.calendar_id = ?
  AND ce.recurrence_rule IS NULL
  AND ce.start_at < ?
  AND ce.end_at > ?
  AND ce.enabled = TRUE
  AND (ce.visibility <> 'confidential' OR ce.owner_user_id = sqlc.arg(viewer_user_id))
ORDER BY ce.start_at ASC, ce.public_id ASC
LIMIT 1000;

-- name: ListRecurringCalendarEventsByRange :many
-- List recurring events whose recurrence window overlaps the query range.
SELECT
  ce.public_id,
  ce.kind,
  ce.visibility,
  ce.show_as,
  ce.flexibility,
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
  uc.public_id AS creator_public_id,
  uc.display_name AS creator_display_name,
  uc.avatar_url AS creator_avatar_url,
  ce.block_label,
  ce.recurrence_rule,
  ce.recurrence_end,
  COALESCE(ce.recurrence_exceptions, CAST('null' AS JSON)) AS recurrence_exceptions,
  ce.notification_offset,
  ce.task_id,
  ce.updated_at,
  ce.created_at,
  EXISTS (
    SELECT 1 FROM calendar_event_attendees a
    WHERE a.event_id = ce.id
      AND a.user_id = sqlc.arg(viewer_user_id)
      AND a.enabled = TRUE
  ) AS is_attendee
FROM calendar_events ce
LEFT JOIN users uc
  ON uc.id = ce.created_by_user_id
WHERE ce.calendar_id = ?
  AND ce.recurrence_rule IS NOT NULL
  AND ce.start_at < ?
  AND (ce.recurrence_end IS NULL OR ce.recurrence_end > ?)
  AND ce.enabled = TRUE
  AND (ce.visibility <> 'confidential' OR ce.owner_user_id = sqlc.arg(viewer_user_id))
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
  ce.flexibility,
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
  ce.created_at,
  EXISTS (
    SELECT 1 FROM calendar_event_attendees a
    WHERE a.event_id = ce.id
      AND a.user_id = sqlc.arg(viewer_user_id)
      AND a.enabled = TRUE
  ) AS is_attendee
FROM calendar_events ce
INNER JOIN calendars c
  ON c.id = ce.calendar_id
INNER JOIN calendar_members cm
  ON cm.calendar_id = ce.calendar_id
  AND cm.user_id = ?
  AND cm.enabled = TRUE
  -- Access is the membership. The subscription only says whether the
  -- member has hidden the layer, so its absence means visible.
  AND NOT EXISTS (
    SELECT 1 FROM calendar_subscriptions cs
    WHERE cs.calendar_id = cm.calendar_id
      AND cs.user_id = cm.user_id
      AND cs.enabled = TRUE
      AND cs.visible = FALSE
  )
WHERE ce.workspace_id = ?
  AND ce.recurrence_rule IS NULL
  AND ce.start_at < ?
  AND ce.end_at > ?
  AND ce.enabled = TRUE
  AND (ce.visibility <> 'confidential' OR ce.owner_user_id = sqlc.arg(viewer_user_id))
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
  ce.flexibility,
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
  COALESCE(ce.recurrence_exceptions, CAST('null' AS JSON)) AS recurrence_exceptions,
  ce.task_id,
  ce.updated_at,
  ce.created_at,
  EXISTS (
    SELECT 1 FROM calendar_event_attendees a
    WHERE a.event_id = ce.id
      AND a.user_id = sqlc.arg(viewer_user_id)
      AND a.enabled = TRUE
  ) AS is_attendee
FROM calendar_events ce
INNER JOIN calendars c
  ON c.id = ce.calendar_id
INNER JOIN calendar_members cm
  ON cm.calendar_id = ce.calendar_id
  AND cm.user_id = ?
  AND cm.enabled = TRUE
  -- Access is the membership. The subscription only says whether the
  -- member has hidden the layer, so its absence means visible.
  AND NOT EXISTS (
    SELECT 1 FROM calendar_subscriptions cs
    WHERE cs.calendar_id = cm.calendar_id
      AND cs.user_id = cm.user_id
      AND cs.enabled = TRUE
      AND cs.visible = FALSE
  )
WHERE ce.workspace_id = ?
  AND ce.recurrence_rule IS NOT NULL
  AND ce.start_at < ?
  AND (ce.recurrence_end IS NULL OR ce.recurrence_end > ?)
  AND ce.enabled = TRUE
  AND (ce.visibility <> 'confidential' OR ce.owner_user_id = sqlc.arg(viewer_user_id))
ORDER BY ce.start_at ASC
LIMIT 1000;

-- name: ListMyCalendarEventsAcrossWorkspaces :many
-- Cross-workspace variant: list non-recurring events on every calendar
-- the caller is subscribed to, across every workspace where the caller
-- is still an active member. workspace_members is joined so that a
-- subscription row lingering past membership removal cannot leak rows
-- (belt-and-braces beyond the soft-disable cascade). Backs GET
-- /me/calendar-events for the unified flow-web calendar so the client
-- does not fan out one request per workspace.
SELECT
  ce.public_id,
  ce.calendar_id,
  c.public_id AS calendar_public_id,
  ce.workspace_id,
  w.public_id AS workspace_public_id,
  w.name AS workspace_name,
  ce.kind,
  ce.visibility,
  ce.show_as,
  ce.flexibility,
  ce.title,
  ce.all_day,
  ce.start_at,
  ce.end_at,
  ce.timezone,
  ce.location,
  ce.owner_user_id,
  uo.public_id AS owner_public_id,
  uc.public_id AS creator_public_id,
  uc.display_name AS creator_display_name,
  uc.avatar_url AS creator_avatar_url,
  (SELECT COUNT(*) FROM calendar_event_attendees a
     WHERE a.event_id = ce.id AND a.enabled = TRUE) AS attendee_count,
  EXISTS(SELECT 1 FROM calendar_event_attendees a
     WHERE a.event_id = ce.id AND a.user_id = wm.user_id AND a.enabled = TRUE) AS viewer_attending,
  ce.block_label,
  ce.task_id,
  ce.updated_at,
  ce.created_at,
  EXISTS (
    SELECT 1 FROM calendar_event_attendees a
    WHERE a.event_id = ce.id
      AND a.user_id = sqlc.arg(viewer_user_id)
      AND a.enabled = TRUE
  ) AS is_attendee
FROM calendar_events ce
INNER JOIN calendars c
  ON c.id = ce.calendar_id AND c.enabled = TRUE
INNER JOIN workspaces w
  ON w.id = ce.workspace_id AND w.enabled = TRUE
INNER JOIN users uo
  ON uo.id = ce.owner_user_id AND uo.enabled = TRUE
LEFT JOIN users uc
  ON uc.id = ce.created_by_user_id
INNER JOIN workspace_members wm
  ON wm.workspace_id = ce.workspace_id
  AND wm.user_id = ?
  AND wm.enabled = TRUE
INNER JOIN calendar_members cm
  ON cm.calendar_id = ce.calendar_id
  AND cm.user_id = wm.user_id
  AND cm.enabled = TRUE
  -- Access is the membership. The subscription only says whether the
  -- member has hidden the layer, so its absence means visible.
  AND NOT EXISTS (
    SELECT 1 FROM calendar_subscriptions cs
    WHERE cs.calendar_id = cm.calendar_id
      AND cs.user_id = cm.user_id
      AND cs.enabled = TRUE
      AND cs.visible = FALSE
  )
WHERE ce.recurrence_rule IS NULL
  AND ce.start_at IS NOT NULL
  AND ce.start_at < ?
  AND ce.end_at > ?
  AND ce.enabled = TRUE
  AND (ce.visibility <> 'confidential' OR ce.owner_user_id = sqlc.arg(viewer_user_id))
ORDER BY ce.start_at ASC, ce.public_id ASC
LIMIT 2000;

-- name: ListMyRecurringCalendarEventsAcrossWorkspaces :many
-- Cross-workspace recurring variant. Same membership guard as the
-- non-recurring query. Clients expand RRULE instances client-side via
-- the shared recurrence expander.
SELECT
  ce.public_id,
  ce.calendar_id,
  c.public_id AS calendar_public_id,
  ce.workspace_id,
  w.public_id AS workspace_public_id,
  w.name AS workspace_name,
  ce.kind,
  ce.visibility,
  ce.show_as,
  ce.flexibility,
  ce.title,
  ce.all_day,
  ce.start_at,
  ce.end_at,
  ce.timezone,
  ce.location,
  ce.owner_user_id,
  uo.public_id AS owner_public_id,
  uc.public_id AS creator_public_id,
  uc.display_name AS creator_display_name,
  uc.avatar_url AS creator_avatar_url,
  (SELECT COUNT(*) FROM calendar_event_attendees a
     WHERE a.event_id = ce.id AND a.enabled = TRUE) AS attendee_count,
  EXISTS(SELECT 1 FROM calendar_event_attendees a
     WHERE a.event_id = ce.id AND a.user_id = wm.user_id AND a.enabled = TRUE) AS viewer_attending,
  ce.block_label,
  ce.recurrence_rule,
  ce.recurrence_end,
  COALESCE(ce.recurrence_exceptions, CAST('null' AS JSON)) AS recurrence_exceptions,
  ce.task_id,
  ce.updated_at,
  ce.created_at,
  EXISTS (
    SELECT 1 FROM calendar_event_attendees a
    WHERE a.event_id = ce.id
      AND a.user_id = sqlc.arg(viewer_user_id)
      AND a.enabled = TRUE
  ) AS is_attendee
FROM calendar_events ce
INNER JOIN calendars c
  ON c.id = ce.calendar_id AND c.enabled = TRUE
INNER JOIN workspaces w
  ON w.id = ce.workspace_id AND w.enabled = TRUE
INNER JOIN users uo
  ON uo.id = ce.owner_user_id AND uo.enabled = TRUE
LEFT JOIN users uc
  ON uc.id = ce.created_by_user_id
INNER JOIN workspace_members wm
  ON wm.workspace_id = ce.workspace_id
  AND wm.user_id = ?
  AND wm.enabled = TRUE
INNER JOIN calendar_members cm
  ON cm.calendar_id = ce.calendar_id
  AND cm.user_id = wm.user_id
  AND cm.enabled = TRUE
  -- Access is the membership. The subscription only says whether the
  -- member has hidden the layer, so its absence means visible.
  AND NOT EXISTS (
    SELECT 1 FROM calendar_subscriptions cs
    WHERE cs.calendar_id = cm.calendar_id
      AND cs.user_id = cm.user_id
      AND cs.enabled = TRUE
      AND cs.visible = FALSE
  )
WHERE ce.recurrence_rule IS NOT NULL
  AND ce.start_at IS NOT NULL
  AND ce.start_at < ?
  AND (ce.recurrence_end IS NULL OR ce.recurrence_end > ?)
  AND ce.enabled = TRUE
  AND (ce.visibility <> 'confidential' OR ce.owner_user_id = sqlc.arg(viewer_user_id))
ORDER BY ce.start_at ASC, ce.public_id ASC
LIMIT 2000;

-- name: PatchCalendarEvent :exec
-- Patch mutable event fields. NULL params leave columns untouched.
UPDATE calendar_events
SET kind                = COALESCE(sqlc.narg('kind'), kind),
    visibility          = COALESCE(sqlc.narg('visibility'), visibility),
    show_as             = COALESCE(sqlc.narg('show_as'), show_as),
    flexibility         = COALESCE(sqlc.narg('flexibility'), flexibility),
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
-- Soft-delete a calendar event by clearing the enabled flag. enabled=FALSE
-- gates LIST/GET reads; the column doubles as the auditable soft-delete
-- marker (no separate deleted_at column).
UPDATE calendar_events
SET enabled = FALSE
WHERE public_id = ?
  AND calendar_id = ?
  AND workspace_id = ?
  AND enabled = TRUE;

-- ListAllCalendarEvents was consumed only by the deleted ICS export path;
-- the replacement will query via calendar_public_shares.

-- name: FindCalendarEventOwner :one
-- Quick lookup for permission checks: who owns this event?
SELECT owner_user_id, calendar_id, workspace_id
FROM calendar_events
WHERE public_id = ?
  AND workspace_id = ?
  AND enabled = TRUE
LIMIT 1;
