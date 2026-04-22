-- v_task_detail
-- Detailed task projection. Uses correlated subqueries for counts to
-- avoid Cartesian products from multiple LEFT JOINs on 1:N tables.
CREATE OR REPLACE VIEW v_task_detail AS
SELECT
  t.workspace_id,
  w.public_id AS workspace_public_id,
  t.public_id,
  p.public_id AS project_public_id,
  p.name AS project_name,
  p.identifier AS project_identifier,
  t.task_number,
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
  t.archived_at,
  (SELECT COUNT(*) FROM task_constraints c WHERE c.task_id = t.id AND c.enabled = TRUE) AS constraint_count,
  (SELECT COUNT(*) FROM task_constraints c WHERE c.task_id = t.id AND c.enabled = TRUE AND c.satisfied_at IS NOT NULL) AS constraint_satisfied_count,
  (SELECT COUNT(*) FROM task_dependencies d WHERE d.from_task_id = t.id AND d.enabled = TRUE) AS dependency_count,
  (SELECT COUNT(*) FROM task_actors a WHERE a.task_id = t.id AND a.enabled = TRUE) AS actor_count,
  (SELECT COUNT(*) FROM task_labels tl WHERE tl.task_id = t.id AND tl.enabled = TRUE) AS label_count,
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
WHERE t.enabled = TRUE
  AND t.archived_at IS NULL;
