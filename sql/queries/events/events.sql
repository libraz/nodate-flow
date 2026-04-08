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

-- name: ListTransitionEventsForReplay :many
-- Ordered list of task.transition.* events for a single task,
-- ascending by occurred_at + id. Used by the Phase 3 replay tool
-- (3.ENG-1) to derive the expected derived_state from scratch.
SELECT type, occurred_at
FROM events
WHERE workspace_id = ?
  AND task_id = ?
  AND type LIKE 'task.transition.%'
ORDER BY occurred_at ASC, id ASC;

-- name: ListEventsForProject :many
-- List a project's timeline via v_task_timeline. Filters events whose
-- owning task lives in the given project (events with no task_id are
-- excluded by virtue of project_public_id being NULL).
SELECT
  v.public_id,
  v.task_public_id,
  v.project_public_id,
  v.actor_user_public_id,
  v.actor_display_name,
  v.type,
  v.payload_json,
  v.occurred_at,
  COUNT(*) OVER() AS total
FROM v_task_timeline v
WHERE v.workspace_id = ?
  AND v.project_public_id = ?
ORDER BY v.occurred_at DESC, v.event_id DESC
LIMIT ? OFFSET ?;

-- name: ListPendingAiSuggestions :many
-- List pending AI suggestions for a workspace. A suggestion is "pending"
-- when an ai.suggestion.proposed event exists with no later
-- ai.suggestion.applied / ai.suggestion.dismissed event for the same
-- inbox_item_id (compared via JSON_EXTRACT on payload_json).
SELECT
  e.public_id,
  e.occurred_at,
  e.payload_json
FROM events e
WHERE e.workspace_id = ?
  AND e.type = 'ai.suggestion.proposed'
  AND NOT EXISTS (
    SELECT 1 FROM events e2
    WHERE e2.workspace_id = e.workspace_id
      AND e2.type IN ('ai.suggestion.applied', 'ai.suggestion.dismissed')
      AND e2.id > e.id
      AND JSON_EXTRACT(e2.payload_json, '$.inbox_item_id') = JSON_EXTRACT(e.payload_json, '$.inbox_item_id')
  )
ORDER BY e.occurred_at DESC
LIMIT 100;

-- name: CountAiSuggestionOutcomesForWorkspace :one
-- Count ai.suggestion.{proposed,applied,dismissed} events for a workspace
-- within the given time window. Used by the AI metrics endpoint
-- (2.OBS-1) to compute acceptance rate.
SELECT
  COALESCE(SUM(CASE WHEN type = 'ai.suggestion.proposed'  THEN 1 ELSE 0 END), 0) AS proposed,
  COALESCE(SUM(CASE WHEN type = 'ai.suggestion.applied'   THEN 1 ELSE 0 END), 0) AS applied,
  COALESCE(SUM(CASE WHEN type = 'ai.suggestion.dismissed' THEN 1 ELSE 0 END), 0) AS dismissed
FROM events
WHERE workspace_id = ?
  AND occurred_at >= ?
  AND type IN ('ai.suggestion.proposed', 'ai.suggestion.applied', 'ai.suggestion.dismissed');

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
