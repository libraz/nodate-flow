-- v_my_tasks
-- Tasks where a user is attached as an actor. Consumers filter on
-- user_public_id in addition to workspace_id.
CREATE OR REPLACE VIEW v_my_tasks AS
SELECT
  t.workspace_id,
  t.public_id,
  u.public_id AS user_public_id,
  p.public_id AS project_public_id,
  p.name AS project_name,
  t.title,
  t.derived_state,
  t.priority,
  t.due_on,
  t.started_on,
  t.event_on,
  a.role AS actor_role,
  t.updated_at,
  t.created_at
FROM task_actors a
INNER JOIN tasks t
  ON t.id = a.task_id AND t.enabled = TRUE
INNER JOIN projects p
  ON p.id = t.project_id AND p.enabled = TRUE
INNER JOIN users u
  ON u.id = a.user_id AND u.enabled = TRUE
INNER JOIN workspaces w
  ON w.id = t.workspace_id AND w.enabled = TRUE
WHERE a.enabled = TRUE;
