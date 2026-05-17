-- name: AddDependency :execlastid
-- Add a directed dependency between two tasks.
INSERT INTO task_dependencies (
  public_id,
  workspace_id,
  from_task_id,
  to_task_id,
  kind
) VALUES (?, ?, ?, ?, ?);

-- name: ListDependenciesForTask :many
-- List outgoing dependencies of a task. Returns the target task public_id.
SELECT
  td.public_id,
  td.kind,
  ft.public_id AS from_task_public_id,
  tt.public_id AS to_task_public_id,
  tt.title    AS to_task_title,
  tt.derived_state AS to_task_derived_state,
  td.updated_at,
  td.created_at,
  COUNT(*) OVER() AS total
FROM task_dependencies td
INNER JOIN tasks ft ON ft.id = td.from_task_id AND ft.enabled = TRUE
INNER JOIN tasks tt ON tt.id = td.to_task_id   AND tt.enabled = TRUE
WHERE td.workspace_id = ?
  AND ft.public_id = ?
  AND td.enabled = TRUE
ORDER BY td.created_at ASC, td.public_id ASC
LIMIT ? OFFSET ?;

-- name: ListIncomingDependenciesForTask :many
-- List incoming dependencies of a task (edges that point AT this task).
SELECT
  td.public_id,
  td.kind,
  ft.public_id AS from_task_public_id,
  ft.title    AS from_task_title,
  ft.derived_state AS from_task_derived_state,
  tt.public_id AS to_task_public_id,
  td.updated_at,
  td.created_at,
  COUNT(*) OVER() AS total
FROM task_dependencies td
INNER JOIN tasks ft ON ft.id = td.from_task_id AND ft.enabled = TRUE
INNER JOIN tasks tt ON tt.id = td.to_task_id   AND tt.enabled = TRUE
WHERE td.workspace_id = ?
  AND tt.public_id = ?
  AND td.enabled = TRUE
ORDER BY td.created_at ASC, td.public_id ASC
LIMIT ? OFFSET ?;

-- name: ListDependenciesForProject :many
-- List every task_dependencies edge where BOTH endpoints belong to the
-- given project. Used by the Gantt chart (to draw dependency arrows)
-- and by the List / Board views (to compute "blocked by open" badges).
SELECT
  td.public_id,
  td.kind,
  ft.public_id AS from_task_public_id,
  ft.derived_state AS from_task_derived_state,
  tt.public_id AS to_task_public_id,
  tt.derived_state AS to_task_derived_state
FROM task_dependencies td
INNER JOIN tasks ft ON ft.id = td.from_task_id AND ft.enabled = TRUE
INNER JOIN tasks tt ON tt.id = td.to_task_id   AND tt.enabled = TRUE
INNER JOIN projects p ON p.id = ft.project_id AND p.enabled = TRUE
WHERE td.workspace_id = ?
  AND p.public_id = ?
  AND ft.project_id = tt.project_id
  AND td.enabled = TRUE
ORDER BY td.created_at ASC, td.public_id ASC
LIMIT 5000;

-- name: DeleteDependency :exec
-- Soft-delete a dependency edge.
UPDATE task_dependencies
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;

-- name: ListRetroDraftsForWorkspace :many
-- List draft retrospective tasks: tasks linked back to a source task
-- via a task_dependencies row with kind='retro_of'. Backs the Phase 6 / L2
-- retro draft queue endpoint (GET /workspaces/{wsId}/tasks/drafts?reason=retro).
-- The retro task itself is the from_task; to_task is the original task whose
-- lifecycle prompted the retrospective. Ordered newest-first so the queue
-- surfaces the freshest drafts at the top.
-- The internal task_id is returned so the handler can resolve the optional
-- created_by_agent attribution via FindRetroDraftAgent without re-issuing
-- a public_id -> id lookup per row (the json:"-" tag on *.id keeps it
-- out of the wire response).
SELECT
  t.id             AS task_id,
  t.public_id      AS task_public_id,
  t.title,
  t.description,
  t.created_at,
  src.public_id    AS source_task_public_id,
  src.title        AS source_task_title,
  COUNT(*) OVER()  AS total
FROM tasks t
INNER JOIN task_dependencies td
  ON td.from_task_id = t.id
 AND td.kind = 'retro_of'
 AND td.enabled = TRUE
INNER JOIN tasks src
  ON src.id = td.to_task_id
WHERE t.workspace_id = ?
  AND t.enabled = TRUE
  -- Archived drafts must not surface in the queue: Discard archives the
  -- task (POST /tasks/{id}/archive) and the UI expects the row to drop
  -- out immediately. Without this filter, archive_at flips to non-null
  -- but the row would still be returned by the next refetch — the
  -- optimistic UI removal would visually rubber-band back.
  AND t.archived_at IS NULL
ORDER BY t.created_at DESC, t.public_id DESC
LIMIT ? OFFSET ?;

-- name: FindRetroDraftAgent :one
-- Resolve the AI agent that produced a retro draft task by looking up the
-- TaskRetroDrafted event (type='task.retro.drafted') joined to ai_agents.
-- Returns the agent's public_id and display name. Used by the retro draft
-- queue handler to enrich rows with createdByAgentId / createdByAgentName
-- without coupling the main list query to ai_agents (the event-derived
-- attribution is the only authoritative source — tasks rows do not carry
-- created_by_agent_id).
SELECT
  a.public_id AS agent_public_id,
  a.name      AS agent_name
FROM events e
INNER JOIN ai_agents a
  ON a.id = e.actor_agent_id
 AND a.enabled = TRUE
WHERE e.workspace_id = ?
  AND e.task_id      = ?
  AND e.type         = 'task.retro.drafted'
  AND e.enabled      = TRUE
  AND e.actor_agent_id IS NOT NULL
ORDER BY e.occurred_at ASC, e.id ASC
LIMIT 1;
