-- name: ListAgentRunsByTask :many
-- List recent agent run events scoped to a single task. The append-only
-- events table is the source of truth for run history; agent_runs rows are
-- a transient scheduler queue and the orchestrator does not stamp a back-
-- pointer from event payloads to a specific agent_runs.public_id today, so
-- a JOIN against agent_runs would only ever return its current pending /
-- claimed slice. Filtering events instead gives the full historical record
-- (started + completed + failed) and survives PurgeFinishedAgentRuns.
--
-- Filter: actor_agent_id IS NOT NULL constrains rows to agent-produced
-- events; the type LIKE narrows to the run lifecycle family. ORDER BY
-- occurred_at DESC, id DESC produces a stable newest-first timeline.
SELECT
  e.public_id,
  e.task_id,
  e.actor_agent_id,
  a.public_id AS agent_public_id,
  a.name      AS agent_name,
  e.type,
  e.payload_json,
  e.occurred_at,
  COUNT(*) OVER() AS total
FROM events e
LEFT JOIN ai_agents a
  ON a.id = e.actor_agent_id AND a.enabled = TRUE
WHERE e.workspace_id = ?
  AND e.task_id = ?
  AND e.actor_agent_id IS NOT NULL
  AND e.type LIKE 'ai.agent.run.%'
ORDER BY e.occurred_at DESC, e.id DESC
LIMIT ?;
