-- Cross-domain count of active calendar events linked to a task.
-- Lives in the tasks query package so the task GET hot path keeps a single
-- generated Queries surface even when the calendars query glob narrows.

-- name: CountActiveCalendarEventsByTaskId :one
-- Count enabled calendar events linked to a specific task (internal id).
-- workspace_id is intentionally omitted: task.id is internal and already
-- scoped to its owning workspace, so the FK on calendar_events.task_id
-- transitively constrains the count. Direct table query (no view) because
-- this is a simple single-row count on a hot path during task GET.
SELECT COUNT(*)
FROM calendar_events
WHERE task_id = ?
  AND enabled = TRUE;
