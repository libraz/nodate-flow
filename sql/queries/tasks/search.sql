-- name: SearchTasks :many
-- Search tasks by title or description using LIKE. Workspace-scoped.
-- The caller supplies the pattern already wrapped in '%…%'.
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
INNER JOIN tasks t
  ON t.public_id = v.public_id AND t.workspace_id = v.workspace_id
WHERE v.workspace_id = ?
  AND (v.title LIKE ? OR t.description LIKE ?)
ORDER BY v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;
