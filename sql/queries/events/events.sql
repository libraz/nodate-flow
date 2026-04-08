-- name: AppendEvent :execlastid
-- Append a single event to the append-only event log. The events table has
-- no UPDATE/DELETE path; only purgeWorkspace removes rows.
INSERT INTO events (
  public_id,
  workspace_id,
  task_id,
  actor_user_id,
  type,
  payload_json,
  occurred_at
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListEventsForTask :many
-- List a task's timeline via v_task_timeline.
SELECT
  v.public_id,
  v.task_public_id,
  v.actor_user_public_id,
  v.actor_display_name,
  v.type,
  v.payload_json,
  v.occurred_at,
  COUNT(*) OVER() AS total
FROM v_task_timeline v
WHERE v.workspace_id = ?
  AND v.task_public_id = ?
ORDER BY v.occurred_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ListEventsForWorkspace :many
-- List the workspace-wide event timeline via v_task_timeline.
SELECT
  v.public_id,
  v.task_public_id,
  v.actor_user_public_id,
  v.actor_display_name,
  v.type,
  v.payload_json,
  v.occurred_at,
  COUNT(*) OVER() AS total
FROM v_task_timeline v
WHERE v.workspace_id = ?
ORDER BY v.occurred_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;
