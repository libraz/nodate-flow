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
  COUNT(*) OVER() AS total
FROM v_task_list v
WHERE v.workspace_id = ?
  AND v.project_public_id = ?
ORDER BY v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
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
  COUNT(*) OVER() AS total
FROM v_task_list v
WHERE v.workspace_id = ?
ORDER BY v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: UpdateTask :exec
-- Update mutable task fields. derived_state is intentionally NOT writable.
UPDATE tasks
SET title = ?,
    description = ?,
    priority = ?,
    due_on = ?,
    started_on = ?
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
