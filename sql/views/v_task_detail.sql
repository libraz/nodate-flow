-- v_task_detail
-- Detailed task projection. Aggregates constraint / dependency / actor
-- counts; full lists are fetched via dedicated queries.
CREATE OR REPLACE VIEW v_task_detail AS
SELECT
  t.workspace_id,
  w.public_id AS workspace_public_id,
  t.public_id,
  p.public_id AS project_public_id,
  p.name AS project_name,
  pt.public_id AS parent_task_public_id,
  creator.public_id AS created_by_user_public_id,
  t.title,
  t.description,
  t.visibility,
  t.derived_state,
  t.priority,
  t.due_on,
  t.started_on,
  t.completed_at,
  COUNT(DISTINCT c.id) AS constraint_count,
  COUNT(DISTINCT CASE WHEN c.satisfied_at IS NOT NULL THEN c.id END) AS constraint_satisfied_count,
  COUNT(DISTINCT d.id) AS dependency_count,
  COUNT(DISTINCT a.id) AS actor_count,
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
LEFT JOIN users creator
  ON creator.id = t.created_by_user_id AND creator.enabled = TRUE
LEFT JOIN task_constraints c
  ON c.task_id = t.id AND c.enabled = TRUE
LEFT JOIN task_dependencies d
  ON d.from_task_id = t.id AND d.enabled = TRUE
LEFT JOIN task_actors a
  ON a.task_id = t.id AND a.enabled = TRUE
WHERE t.enabled = TRUE
GROUP BY
  t.workspace_id,
  w.public_id,
  t.public_id,
  p.public_id,
  p.name,
  pt.public_id,
  creator.public_id,
  t.title,
  t.description,
  t.visibility,
  t.derived_state,
  t.priority,
  t.due_on,
  t.started_on,
  t.completed_at,
  t.sort_weight,
  t.updated_at,
  t.created_at;
