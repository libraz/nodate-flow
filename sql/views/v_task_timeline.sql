-- v_task_timeline
-- Timeline of events related to tasks. Merges events with the owning
-- task's public_id so the UI can render without touching internal ids.
CREATE OR REPLACE VIEW v_task_timeline AS
SELECT
  e.workspace_id,
  e.id AS event_id,
  e.public_id,
  t.public_id AS task_public_id,
  p.public_id AS project_public_id,
  actor.public_id AS actor_user_public_id,
  actor.display_name AS actor_display_name,
  agent.public_id AS actor_agent_public_id,
  CAST(COALESCE(agent.name, '') AS CHAR(255)) AS actor_agent_name,
  e.type,
  e.payload_json,
  e.occurred_at
FROM events e
INNER JOIN workspaces w
  ON w.id = e.workspace_id AND w.enabled = TRUE
LEFT JOIN tasks t
  ON t.id = e.task_id AND t.enabled = TRUE
LEFT JOIN projects p
  ON p.id = t.project_id AND p.enabled = TRUE
LEFT JOIN users actor
  ON actor.id = e.actor_user_id AND actor.enabled = TRUE
LEFT JOIN ai_agents agent
  ON agent.id = e.actor_agent_id AND agent.enabled = TRUE
WHERE e.enabled = TRUE;
