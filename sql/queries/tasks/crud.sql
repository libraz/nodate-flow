-- name: CreateTask :execlastid
-- Insert a new task. derived_state defaults to 'open' and must NOT be set
-- directly here; the constraint engine and event bus mutate it.
INSERT INTO tasks (
  public_id,
  workspace_id,
  project_id,
  parent_task_id,
  created_by_user_id,
  title,
  description,
  priority,
  due_on,
  started_on
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindTaskByPublicId :one
-- Detail projection via v_task_detail. Workspace-scoped.
SELECT
  v.public_id,
  v.workspace_public_id,
  v.project_public_id,
  v.project_name,
  v.parent_task_public_id,
  v.created_by_user_public_id,
  v.title,
  v.description,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.completed_at,
  v.constraint_count,
  v.constraint_satisfied_count,
  v.dependency_count,
  v.actor_count,
  v.sort_weight,
  v.updated_at,
  v.created_at
FROM v_task_detail v
WHERE v.workspace_id = ?
  AND v.public_id = ?
LIMIT 1;

-- name: ListTasksForProject :many
-- List tasks in a project via v_task_list with window-function pagination.
SELECT
  v.public_id,
  v.project_public_id,
  v.project_name,
  v.parent_task_public_id,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.completed_at,
  v.sort_weight,
  v.updated_at,
  v.created_at,
  v.primary_assignee_public_id,
  v.assignee_count,
  COUNT(*) OVER() AS total
FROM v_task_list v
WHERE v.workspace_id = ?
  AND v.project_public_id = ?
ORDER BY v.sort_weight ASC, v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ListTasksForWorkspace :many
-- List tasks across an entire workspace via v_task_list.
SELECT
  v.public_id,
  v.project_public_id,
  v.project_name,
  v.parent_task_public_id,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.completed_at,
  v.sort_weight,
  v.updated_at,
  v.created_at,
  v.primary_assignee_public_id,
  v.assignee_count,
  COUNT(*) OVER() AS total
FROM v_task_list v
WHERE v.workspace_id = ?
ORDER BY v.sort_weight ASC, v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: UpdateTask :exec
-- Update mutable task fields. derived_state is intentionally NOT writable.
UPDATE tasks
SET title = ?,
    description = ?,
    priority = ?,
    due_on = ?,
    started_on = ?,
    sort_weight = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: UpdateTaskSortWeight :exec
-- Update only the sort_weight for a single task within a workspace.
-- Used by the bulk reorder endpoint inside a transaction.
UPDATE tasks
SET sort_weight = ?
WHERE id = ?
  AND workspace_id = ?
  AND enabled = TRUE;

-- name: TransitionTaskState :exec
-- Write the new derived_state computed by the transition handler. This is
-- the only path allowed to mutate derived_state and must be called inside
-- the same transaction as the events append.
UPDATE tasks
SET derived_state = ?,
    completed_at = CASE WHEN ? = 'done' THEN CURRENT_TIMESTAMP ELSE completed_at END
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: DisableTask :exec
-- Soft-disable a task.
UPDATE tasks
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;

-- name: ListMyTasks :many
-- Tasks where the given user is attached as an actor, via v_my_tasks.
SELECT
  v.public_id,
  v.project_public_id,
  v.project_name,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  v.actor_role,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_my_tasks v
WHERE v.workspace_id = ?
  AND v.user_public_id = ?
ORDER BY v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ListMyTasksGlobal :many
-- Cross-workspace variant: tasks where the given user is attached as an
-- actor across every workspace they belong to, joined with the workspace
-- row so the caller gets workspace_public_id / name for grouping. Used by
-- GET /me/tasks to power the cross-workspace "Today" / Calendar views in
-- the web client without fanning out one request per workspace.
SELECT
  v.public_id,
  w.public_id AS workspace_public_id,
  w.name AS workspace_name,
  v.project_public_id,
  v.project_name,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  v.actor_role,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_my_tasks v
INNER JOIN workspaces w
  ON w.id = v.workspace_id AND w.enabled = TRUE
WHERE v.user_public_id = ?
ORDER BY v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;
