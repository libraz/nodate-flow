-- name: CreateTaskEventLink :execlastid
-- Insert a new link between a task and an event. Caller must validate
-- the task and event belong to workspace_id and are enabled.
INSERT INTO task_event_links (
  public_id,
  workspace_id,
  task_id,
  event_id,
  relation,
  sort_weight
) VALUES (?, ?, ?, ?, ?, ?);

-- name: FindTaskEventLinkByPublicId :one
-- Resolve a single link (enabled only) inside a workspace.
SELECT
  id,
  public_id,
  workspace_id,
  task_id,
  event_id,
  relation,
  sort_weight,
  enabled,
  updated_at,
  created_at
FROM task_event_links
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: DeleteTaskEventLink :execrows
-- Soft-delete a link by public id within a workspace.
UPDATE task_event_links
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: ListLinkedEventsForTask :many
-- List the events a task is linked to (optionally filtered by relation).
-- @relation may be an empty string to include all relations.
--
-- A task and the calendars its links point at have separate audiences: a
-- workspace holds calendars whose member lists do not coincide, so
-- reaching the task says nothing about reaching the events hanging off
-- it. Without the membership join below, this list hands every reader of
-- the task the title, times and calendar name of events on calendars they
-- cannot open.
--
-- The join is the whole rule and it lives here rather than in the caller,
-- so the rows and the total that describes them come out of one
-- statement. Membership is read the way ListCalendarsForUser reads it —
-- an enabled calendar_members row at any role, on an enabled calendar —
-- so a calendar this list sees through is one the event routes open too.
--
-- A link the actor cannot see through is returned rather than dropped.
-- The link, its relation and the fact that the task has it are the task's
-- own data, which this caller may read; what belongs to the calendar is
-- the event's title, its times and the calendar's name. event_hidden
-- carries the verdict per row, and the row's calendar-owned fields are
-- the ones the caller withholds from the response — a reader must not
-- have to read an empty title as an untitled event. Dropping the row
-- instead would make the task look like it has fewer links than it has.
--
-- The verdict is a column rather than a redaction written into the SELECT
-- list because wrapping a column in an expression costs it its type: an
-- IF() over ce.title or ce.start_at generates as an untyped value, and a
-- response assembled out of those is one type assertion away from
-- rendering a time as nothing at all.
--
-- The two public ids are not withheld anywhere: a UUID names a row
-- without describing it, and every route that accepts one applies this
-- same membership floor.
SELECT
  tel.public_id   AS link_public_id,
  tel.relation,
  tel.sort_weight,
  tel.created_at  AS link_created_at,
  cm.id IS NULL   AS event_hidden,
  ce.public_id    AS event_public_id,
  ce.title        AS event_title,
  ce.start_at,
  ce.end_at,
  ce.all_day,
  ce.timezone,
  c.public_id     AS calendar_public_id,
  c.name          AS calendar_name,
  COUNT(*) OVER() AS total
FROM task_event_links tel
INNER JOIN tasks t ON t.id = tel.task_id AND t.enabled = TRUE
INNER JOIN calendar_events ce ON ce.id = tel.event_id AND ce.enabled = TRUE
INNER JOIN calendars c ON c.id = ce.calendar_id AND c.enabled = TRUE
LEFT JOIN calendar_members cm
  ON cm.calendar_id = c.id
 AND cm.workspace_id = tel.workspace_id
 AND cm.user_id = sqlc.arg('actor_user_id')
 AND cm.enabled = TRUE
WHERE tel.workspace_id = ?
  AND t.public_id = ?
  AND tel.enabled = TRUE
  AND (sqlc.arg('relation') = '' OR tel.relation = sqlc.arg('relation'))
ORDER BY tel.sort_weight ASC, tel.created_at ASC, tel.public_id ASC
LIMIT ? OFFSET ?;

-- name: ListLinkedTasksForEvent :many
-- List the tasks linked to an event (optionally filtered by relation).
SELECT
  tel.public_id   AS link_public_id,
  tel.relation,
  tel.sort_weight,
  tel.created_at  AS link_created_at,
  t.public_id     AS task_public_id,
  t.title         AS task_title,
  t.derived_state AS task_derived_state,
  t.due_on        AS task_due_on,
  COUNT(*) OVER() AS total
FROM task_event_links tel
INNER JOIN tasks t ON t.id = tel.task_id AND t.enabled = TRUE
INNER JOIN calendar_events ce ON ce.id = tel.event_id AND ce.enabled = TRUE
WHERE tel.workspace_id = sqlc.arg('workspace_id')
  AND ce.public_id = sqlc.arg('event_public_id')
  AND tel.enabled = TRUE
  AND (sqlc.arg('relation') = '' OR tel.relation = sqlc.arg('relation'))
  -- The task's title is on the wire and an event id is reachable by any
  -- workspace member, so the link is listable only when the actor may
  -- see the task at its far end. Elevated roles skip the check.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR t.visibility = 'public'
    OR (t.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_t
      WHERE pm_t.project_id = t.project_id
        AND pm_t.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_t.enabled = TRUE
    ))
    OR (t.visibility = 'private' AND (
      t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_t
        WHERE ta_t.task_id = t.id
          AND ta_t.kind = 'user'
          AND ta_t.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_t.enabled = TRUE
      )
    ))
  )
ORDER BY tel.sort_weight ASC, tel.created_at ASC, tel.public_id ASC
LIMIT ? OFFSET ?;

-- name: FindActiveLink :one
-- Lookup the single active (task, event, relation) tuple. Used by
-- itemkit to detect duplicates before attempting to insert.
SELECT
  id,
  public_id,
  workspace_id,
  task_id,
  event_id,
  relation,
  enabled,
  created_at
FROM task_event_links
WHERE workspace_id = ?
  AND task_id = ?
  AND event_id = ?
  AND relation = ?
  AND enabled = TRUE
LIMIT 1;
