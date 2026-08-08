-- name: CreateAgent :execlastid
-- Insert a new reusable agent configuration.
INSERT INTO ai_agents (
  public_id,
  workspace_id,
  model_id,
  name,
  description,
  system_prompt,
  temperature,
  max_output_tokens,
  tools_json,
  schedule_kind
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAgentsForWorkspace :many
-- List a workspace's agents joined with the underlying model.
SELECT
  a.public_id,
  m.public_id AS model_public_id,
  m.name      AS model_name,
  a.name,
  a.description,
  a.system_prompt,
  a.temperature,
  a.max_output_tokens,
  a.tools_json,
  a.schedule_kind,
  a.paused,
  a.updated_at,
  a.created_at,
  COUNT(*) OVER() AS total
FROM ai_agents a
INNER JOIN ai_models m ON m.id = a.model_id AND m.enabled = TRUE
WHERE a.workspace_id = ?
  AND a.enabled = TRUE
ORDER BY a.created_at DESC, a.public_id DESC
LIMIT ? OFFSET ?;

-- name: UpdateAgentScheduleKind :execrows
-- Update the schedule_kind on an existing agent.
UPDATE ai_agents
SET schedule_kind = ?
WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE;

-- name: ListOnEventAgents :many
-- List every enabled non-paused agent whose event_trigger_types
-- contains the given event kind. Driven by the eventbus notify hook
-- so the fan-out from a single eventbus.Append to N agents is one
-- round-trip per append (vs one per agent).
SELECT
  a.id,
  a.workspace_id,
  a.public_id
FROM ai_agents a
WHERE a.enabled = TRUE
  AND a.paused = FALSE
  AND a.schedule_kind = 'on_event'
  AND a.workspace_id = ?
  AND JSON_CONTAINS(a.event_trigger_types, JSON_QUOTE(?));

-- name: ListOnEventAgentsForEvent :many
-- List every enabled non-paused on_event agent that should wake for a
-- specific appended event. Joins through the events row so the filter
-- can use the event's task_id to enforce ai_agents.schedule_scope:
--   * 'workspace' (default) → fan out to every workspace-scoped agent
--     whose event_trigger_types contains the event kind, matching the
--     legacy ListOnEventAgents semantics.
--   * 'assigned_tasks' → only fan out to agents that are an enabled
--     task_actor (kind='agent') on the event's task. Events without a
--     task_id are skipped for assigned-task agents because there is no
--     task scope to match.
-- Driven by the eventbus notify hook with the inserted event's id, so
-- the fan-out is still one round-trip per append.
SELECT
  a.id,
  a.workspace_id,
  a.public_id
FROM ai_agents a
INNER JOIN events e ON e.id = ? AND e.workspace_id = a.workspace_id
WHERE a.enabled = TRUE
  AND a.paused = FALSE
  AND a.schedule_kind = 'on_event'
  AND a.workspace_id = ?
  AND JSON_CONTAINS(a.event_trigger_types, JSON_QUOTE(e.type))
  AND (
    a.schedule_scope = 'workspace'
    OR (
      a.schedule_scope = 'assigned_tasks'
      AND e.task_id IS NOT NULL
      AND EXISTS (
        SELECT 1 FROM task_actors ta
        WHERE ta.workspace_id = a.workspace_id
          AND ta.task_id = e.task_id
          AND ta.agent_id = a.id
          AND ta.kind = 'agent'
          AND ta.enabled = TRUE
      )
    )
  );

-- name: GetAgentGuardSnapshot :one
-- Fetch the minimal fields the agent guard needs to make an allow/deny
-- decision. Returns enabled, paused, allowed_scopes_json, and the
-- monthly cost cap. Used by the MCP dispatch guard.
SELECT
  enabled,
  paused,
  allowed_scopes_json,
  monthly_cost_cap_cents
FROM ai_agents
WHERE id = ?
LIMIT 1;

-- name: GetAgentForExec :one
-- Fetch the minimal fields an agent runner needs to invoke an LLM.
SELECT
  a.id,
  a.public_id,
  a.workspace_id,
  a.name,
  a.system_prompt,
  a.temperature,
  a.max_output_tokens,
  a.paused,
  m.public_id AS model_public_id,
  m.name AS model_name
FROM ai_agents a
INNER JOIN ai_models m ON m.id = a.model_id AND m.enabled = TRUE
WHERE a.workspace_id = ? AND a.id = ? AND a.enabled = TRUE;
