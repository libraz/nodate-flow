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

-- name: DeleteTaskEventLink :exec
-- Soft-delete a link by public id within a workspace.
UPDATE task_event_links
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;

-- name: ListLinkedEventsForTask :many
-- List the events a task is linked to (optionally filtered by relation).
-- @relation may be an empty string to include all relations.
SELECT
  tel.public_id   AS link_public_id,
  tel.relation,
  tel.sort_weight,
  tel.created_at  AS link_created_at,
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
  t.event_on      AS task_event_on,
  t.due_on        AS task_due_on,
  COUNT(*) OVER() AS total
FROM task_event_links tel
INNER JOIN tasks t ON t.id = tel.task_id AND t.enabled = TRUE
INNER JOIN calendar_events ce ON ce.id = tel.event_id AND ce.enabled = TRUE
WHERE tel.workspace_id = ?
  AND ce.public_id = ?
  AND tel.enabled = TRUE
  AND (sqlc.arg('relation') = '' OR tel.relation = sqlc.arg('relation'))
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
