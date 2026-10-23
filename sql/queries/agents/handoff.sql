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
