-- v_project_stats
-- Aggregated task counts per project, grouped by derived_state.
CREATE OR REPLACE VIEW v_project_stats AS
SELECT
  p.workspace_id,
  p.public_id,
  p.name,
  COUNT(t.id) AS task_count,
  SUM(CASE WHEN t.derived_state = 'open' THEN 1 ELSE 0 END) AS open_count,
  SUM(CASE WHEN t.derived_state = 'waiting' THEN 1 ELSE 0 END) AS waiting_count,
  SUM(CASE WHEN t.derived_state = 'review' THEN 1 ELSE 0 END) AS review_count,
  SUM(CASE WHEN t.derived_state = 'done' THEN 1 ELSE 0 END) AS done_count,
  SUM(CASE WHEN t.derived_state = 'cancelled' THEN 1 ELSE 0 END) AS cancelled_count,
  MAX(t.updated_at) AS last_task_updated_at
FROM projects p
LEFT JOIN tasks t
  ON t.project_id = p.id AND t.enabled = TRUE
INNER JOIN workspaces w
  ON w.id = p.workspace_id AND w.enabled = TRUE
WHERE p.enabled = TRUE
GROUP BY p.workspace_id, p.public_id, p.name;
