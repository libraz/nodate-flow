-- v_task_list
-- Minimal task projection for list / board views.
CREATE OR REPLACE VIEW v_task_list AS
SELECT
  t.workspace_id,
  t.public_id,
  p.public_id AS project_public_id,
  p.name AS project_name,
  pt.public_id AS parent_task_public_id,
  t.title,
  t.derived_state,
  t.priority,
  t.due_on,
  t.started_on,
  t.completed_at,
  t.sort_weight,
  t.updated_at,
  t.created_at
FROM tasks t
INNER JOIN projects p
  ON p.id = t.project_id AND p.enabled = TRUE
INNER JOIN workspaces w
  ON w.id = t.workspace_id AND w.enabled = TRUE
LEFT JOIN tasks pt
  ON pt.id = t.parent_task_id AND pt.enabled = TRUE
WHERE t.enabled = TRUE;
