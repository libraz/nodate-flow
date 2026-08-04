-- name: InsertHandoffToAgentEvent :execlastid
-- Append an agent.task.handoff_to_agent event. The caller is a human (or a
-- system actor) transferring a task to an AI agent, so actor_user_id is
-- populated and actor_agent_id stays NULL. Payload is caller-shaped: the
-- handler embeds the target agent's public_id, the reason string, and any
-- delegation chain context. Mutual exclusion of actor_user_id and
-- actor_agent_id is preserved by this query's column list rather than a
-- CHECK constraint (see sql/core/tables/events.sql).
INSERT INTO events (
  public_id,
  workspace_id,
  task_id,
  actor_user_id,
  type,
  payload_json,
  occurred_at
) VALUES (?, ?, ?, ?, 'agent.task.handoff_to_agent', ?, ?);

-- name: InsertHandoffToUserEvent :execlastid
-- Append an agent.task.handoff_to_user event. The caller is an AI agent
-- handing the task back to a human, so actor_agent_id is populated and
-- actor_user_id stays NULL. Payload is caller-shaped: the handler embeds
-- the target user's public_id, the reason string, and any handoff status
-- recorded in tasks.agent_memo.
INSERT INTO events (
  public_id,
  workspace_id,
  task_id,
  actor_agent_id,
  type,
  payload_json,
  occurred_at
) VALUES (?, ?, ?, ?, 'agent.task.handoff_to_user', ?, ?);
