-- name: ListAgentsAssignedToTask :many
-- List the AI agents currently attached to a task as enabled actors. Used
-- by the agent runtime's scoped event fan-out (schedule_scope =
-- 'assigned_tasks') to enumerate the candidate agents for a given task
-- before deciding which to wake. The chk_task_actors_kind_target check
-- guarantees agent_id is non-null when kind='agent', but the join filters
-- are kept explicit so the planner can use idx_task_actors_task_id_enabled.
SELECT
  a.id,
  a.public_id,
  a.workspace_id,
  a.name,
  a.schedule_kind,
  a.schedule_scope,
  a.event_trigger_types,
  a.paused
FROM task_actors ta
INNER JOIN ai_agents a
  ON a.id = ta.agent_id AND a.enabled = TRUE
WHERE ta.workspace_id = ?
  AND ta.task_id = ?
  AND ta.kind = 'agent'
  AND ta.enabled = TRUE
ORDER BY a.id ASC;

-- name: ListEventsForScopedAgent :many
-- Incremental polling source for an agent whose schedule_scope is
-- 'assigned_tasks'. Returns events newer than since_id whose owning task
-- has this agent as an enabled actor, filtered by the agent's
-- event_trigger_types. The caller passes the agent's internal id and the
-- last id it processed (0 for the first poll). ORDER BY id ASC + LIMIT
-- gives a forward-paginated stream the orchestrator can drain.
--
-- The JSON_CONTAINS check matches the workspace-wide fan-out used by
-- ListOnEventAgents so identical event kinds flow through identical
-- predicates. Events without a task_id (workspace-level signals) are
-- intentionally excluded — scoped agents only react to task-bound events.
SELECT
  e.id,
  e.public_id,
  e.workspace_id,
  e.task_id,
  e.actor_user_id,
  e.actor_agent_id,
  e.type,
  e.payload_json,
  e.occurred_at
FROM events e
INNER JOIN ai_agents a
  ON a.id = ? AND a.enabled = TRUE AND a.paused = FALSE
WHERE e.workspace_id = ?
  AND e.id > ?
  AND e.task_id IS NOT NULL
  AND JSON_CONTAINS(a.event_trigger_types, JSON_QUOTE(e.type))
  AND e.task_id IN (
    SELECT ta.task_id
    FROM task_actors ta
    WHERE ta.workspace_id = e.workspace_id
      AND ta.agent_id = a.id
      AND ta.kind = 'agent'
      AND ta.enabled = TRUE
  )
ORDER BY e.id ASC
LIMIT ?;
