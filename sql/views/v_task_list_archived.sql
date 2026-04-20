-- v_task_list_archived
-- Same as v_task_list but only shows archived tasks.
CREATE OR REPLACE VIEW v_task_list_archived AS
SELECT
  t.workspace_id,
  t.id AS task_internal_id,
  t.project_id,
  t.created_by_user_id,
  t.public_id,
  p.public_id AS project_public_id,
  p.name AS project_name,
  p.identifier AS project_identifier,
  t.task_number,
  pt.public_id AS parent_task_public_id,
  t.title,
  t.visibility,
  t.derived_state,
  t.priority,
  t.due_on,
  t.started_on,
  t.event_on,
  t.completed_at,
  t.archived_at,
  t.sort_weight,
  t.updated_at,
  t.created_at,
  assignees.primary_assignee_public_id,
  COALESCE(assignees.assignee_count, 0) AS assignee_count,
  labels.label_ids
FROM tasks t
INNER JOIN projects p
  ON p.id = t.project_id AND p.enabled = TRUE
INNER JOIN workspaces w
  ON w.id = t.workspace_id AND w.enabled = TRUE
LEFT JOIN tasks pt
  ON pt.id = t.parent_task_id AND pt.enabled = TRUE
LEFT JOIN (
  SELECT
    ta.task_id,
    MIN(CASE WHEN rn = 1 THEN u.public_id END) AS primary_assignee_public_id,
    COUNT(*) AS assignee_count
  FROM (
    SELECT ta2.task_id, ta2.user_id,
           ROW_NUMBER() OVER (PARTITION BY ta2.task_id ORDER BY ta2.sort_weight ASC, ta2.id ASC) AS rn
    FROM task_actors ta2
    WHERE ta2.enabled = TRUE AND ta2.role = 'assignee'
  ) ta
  INNER JOIN users u ON u.id = ta.user_id AND u.enabled = TRUE
  GROUP BY ta.task_id
) assignees ON assignees.task_id = t.id
LEFT JOIN (
  SELECT
    tl.task_id,
    GROUP_CONCAT(l.public_id ORDER BY tl.sort_weight ASC, tl.id ASC SEPARATOR ',') AS label_ids
  FROM task_labels tl
  INNER JOIN labels l ON l.id = tl.label_id AND l.enabled = TRUE
  WHERE tl.enabled = TRUE
  GROUP BY tl.task_id
) labels ON labels.task_id = t.id
WHERE t.enabled = TRUE
  AND t.archived_at IS NOT NULL;
