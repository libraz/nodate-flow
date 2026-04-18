-- v_task_list
-- Minimal task projection for list / board views.
CREATE OR REPLACE VIEW v_task_list AS
SELECT
  t.workspace_id,
  t.id AS task_internal_id,
  t.project_id,
  t.created_by_user_id,
  t.public_id,
  p.public_id AS project_public_id,
  p.name AS project_name,
  pt.public_id AS parent_task_public_id,
  t.title,
  t.visibility,
  t.derived_state,
  t.priority,
  t.due_on,
  t.started_on,
  t.event_on,
  t.completed_at,
  t.sort_weight,
  t.updated_at,
  t.created_at,
  (
    SELECT u.public_id
    FROM task_actors ta
    INNER JOIN users u ON u.id = ta.user_id AND u.enabled = TRUE
    WHERE ta.task_id = t.id
      AND ta.enabled = TRUE
      AND ta.role = 'assignee'
    ORDER BY ta.sort_weight ASC, ta.id ASC
    LIMIT 1
  ) AS primary_assignee_public_id,
  (
    SELECT COUNT(*)
    FROM task_actors ta
    WHERE ta.task_id = t.id
      AND ta.enabled = TRUE
      AND ta.role = 'assignee'
  ) AS assignee_count
FROM tasks t
INNER JOIN projects p
  ON p.id = t.project_id AND p.enabled = TRUE
INNER JOIN workspaces w
  ON w.id = t.workspace_id AND w.enabled = TRUE
LEFT JOIN tasks pt
  ON pt.id = t.parent_task_id AND pt.enabled = TRUE
WHERE t.enabled = TRUE;
