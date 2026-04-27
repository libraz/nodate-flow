-- name: ExportTasksForWorkspace :many
-- Fetch tasks for CSV/JSON export across an entire workspace.
-- Includes project name and primary assignee for human-readable output.
SELECT
  t.public_id,
  t.title,
  t.description,
  t.derived_state,
  t.priority,
  t.due_on,
  t.started_on,
  t.completed_at,
  p.public_id AS project_public_id,
  p.name AS project_name,
  a_user.public_id AS assignee_public_id,
  a_user.display_name AS assignee_display_name,
  t.updated_at,
  t.created_at
FROM tasks t
INNER JOIN projects p ON p.id = t.project_id AND p.enabled = TRUE
LEFT JOIN task_actors ta ON ta.task_id = t.id
  AND ta.role = 'assignee'
  AND ta.kind = 'user'
  AND ta.enabled = TRUE
LEFT JOIN users a_user ON a_user.id = ta.user_id AND a_user.enabled = TRUE
WHERE t.workspace_id = ?
  AND t.enabled = TRUE
ORDER BY t.created_at DESC, t.id DESC
LIMIT ?;

-- name: ExportTasksForLens :many
-- Fetch tasks for export scoped to a workspace + project (lens-scoped).
-- Lens filter/sort logic is applied in Go code; this provides the data set.
SELECT
  t.public_id,
  t.title,
  t.description,
  t.derived_state,
  t.priority,
  t.due_on,
  t.started_on,
  t.completed_at,
  p.public_id AS project_public_id,
  p.name AS project_name,
  a_user.public_id AS assignee_public_id,
  a_user.display_name AS assignee_display_name,
  t.updated_at,
  t.created_at
FROM tasks t
INNER JOIN projects p ON p.id = t.project_id AND p.enabled = TRUE
LEFT JOIN task_actors ta ON ta.task_id = t.id
  AND ta.role = 'assignee'
  AND ta.kind = 'user'
  AND ta.enabled = TRUE
LEFT JOIN users a_user ON a_user.id = ta.user_id AND a_user.enabled = TRUE
WHERE t.workspace_id = ?
  AND t.project_id = ?
  AND t.enabled = TRUE
ORDER BY t.created_at DESC, t.id DESC
LIMIT ?;
