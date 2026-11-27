-- name: ListAgentContextComments :many
-- The newest comments of a public task for an agent's prompt, newest first.
-- author_kind says human or agent; the display name is the user's for a human
-- and the retained byline for an agent. The task join carries the visibility
-- gate: a task that is not public yields nothing.
SELECT
  c.public_id,
  c.author_kind,
  u.display_name AS author_display_name,
  c.author_agent_name,
  c.body,
  c.created_at
FROM comments c
INNER JOIN tasks t ON t.id = c.task_id AND t.workspace_id = c.workspace_id
LEFT JOIN users u ON u.id = c.author_id
WHERE c.workspace_id = sqlc.arg('workspace_id')
  AND c.task_id = sqlc.arg('task_id')
  AND c.enabled = TRUE
  AND t.enabled = TRUE
  AND t.visibility = 'public'
ORDER BY c.created_at DESC, c.id DESC
LIMIT ?;

-- name: ListAgentContextChildTasks :many
-- task-visibility: not-applicable — agent reader; the query requires t.visibility = 'public'
-- Public child tasks of a task for an agent's prompt: title and state only.
SELECT
  t.public_id,
  t.title,
  t.derived_state
FROM tasks t
WHERE t.workspace_id = sqlc.arg('workspace_id')
  AND t.parent_task_id = sqlc.arg('parent_task_id')
  AND t.enabled = TRUE
  AND t.visibility = 'public'
ORDER BY t.created_at ASC, t.id ASC
LIMIT ?;

-- name: GetLatestTransitionEventID :one
-- Highest events.id of a task among the given transition event types; 0 when
-- the task has none. Types are passed in so no event kind is written here.
SELECT CAST(COALESCE(MAX(e.id), 0) AS UNSIGNED) AS latest_event_id
FROM events e
WHERE e.workspace_id = sqlc.arg('workspace_id')
  AND e.task_id = sqlc.arg('task_id')
  AND e.enabled = TRUE
  AND e.type IN (sqlc.slice('transition_types'));

-- name: GetMaxTaskEventID :one
-- Latest enabled event of a task: internal id and public id. No row when the
-- task has none (callers handle sql.ErrNoRows).
SELECT
  e.id AS max_event_id,
  e.public_id AS max_event_public_id
FROM events e
WHERE e.workspace_id = sqlc.arg('workspace_id')
  AND e.task_id = sqlc.arg('task_id')
  AND e.enabled = TRUE
ORDER BY e.id DESC
LIMIT 1;

-- name: GetLatestTaskWorkContextEventID :one
-- Highest internal events.id of the context events (payload contextEventId is
-- the context event's public id) of an agent's finished task_work runs on a
-- task; 0 when none. endedReason is limited to the fixed set of successful
-- endings a later event may coalesce into. Only the run completed event type
-- is read; failed runs never coalesce later events.
SELECT CAST(COALESCE(MAX(ctx.id), 0) AS UNSIGNED) AS context_event_id
FROM events e
INNER JOIN events ctx
  ON ctx.workspace_id = e.workspace_id
  AND ctx.public_id = UUID_TO_BIN(JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.contextEventId')), 0)
WHERE e.workspace_id = sqlc.arg('workspace_id')
  AND e.task_id = sqlc.arg('task_id')
  AND e.actor_agent_id = sqlc.arg('agent_id')
  AND e.type = sqlc.arg('completed_type')
  AND e.enabled = TRUE
  AND JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.mode')) = 'task_work'
  AND JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.endedReason')) IN ('done', 'max_steps', 'write_cap', 'repeat');

-- name: CountTaskWorkRunsSince :one
-- task_work runs an agent started on a task at or after a time (the rate
-- limit window).
SELECT COUNT(*) AS run_count
FROM events e
WHERE e.workspace_id = sqlc.arg('workspace_id')
  AND e.task_id = sqlc.arg('task_id')
  AND e.actor_agent_id = sqlc.arg('agent_id')
  AND e.type = sqlc.arg('started_type')
  AND e.enabled = TRUE
  AND e.occurred_at >= sqlc.arg('since')
  AND JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.mode')) = 'task_work';

-- name: CountAgentCreatedChildren :one
-- Live child tasks of a parent created without a user (the agent path), for
-- the cumulative create_subtask cap.
SELECT COUNT(*) AS child_count
FROM tasks t
WHERE t.workspace_id = sqlc.arg('workspace_id')
  AND t.parent_task_id = sqlc.arg('parent_task_id')
  AND t.created_by_user_id IS NULL
  AND t.enabled = TRUE;

-- name: GetAssigneeDelegator :one
-- The enabled assignee row of an agent on a task. No row means the agent is
-- not assigned; a NULL delegated_by_user_id means no delegator is recorded.
SELECT delegated_by_user_id
FROM task_actors
WHERE workspace_id = sqlc.arg('workspace_id')
  AND task_id = sqlc.arg('task_id')
  AND agent_id = sqlc.arg('agent_id')
  AND kind = 'agent'
  AND role = 'assignee'
  AND enabled = TRUE
LIMIT 1;
