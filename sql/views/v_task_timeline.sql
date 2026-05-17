-- v_task_timeline
-- Timeline of events related to tasks. Merges events with the owning
-- task's public_id so the UI can render without touching internal ids.
--
-- Exposes the third actor source (`actor_system_source`, ADR 0008 D8) and
-- the signal traceability link (`triggered_by_signal_public_id`, ADR 0008
-- D4) so the timeline UI can render "Discord said idle -> judge ran ->
-- task completed" as a single causal chain. The signal join uses alias
-- `tsig` (triggering signal) to avoid colliding with any future signals
-- JOIN that might use the conventional `s` alias.
--
-- Reversal projection (ADR 0008 D4 / J5): `reverses_event_public_id`
-- surfaces the target event's public_id when this row is a compensating
-- reverse, sourced from a self-join aliased `e_rev` (reverse target).
-- `was_reversed` is TRUE when some other enabled event points back to this
-- row via reverses_event_id; the correlated EXISTS subquery uses alias
-- `e_chk` (reverse check) and is backed by idx_events_reverses
-- (workspace_id, reverses_event_id) so the per-row scan stays index-only.
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
  e.actor_system_source,
  tsig.public_id AS triggered_by_signal_public_id,
  e_rev.public_id AS reverses_event_public_id,
  EXISTS (
    SELECT 1 FROM events e_chk
    WHERE e_chk.workspace_id = e.workspace_id
      AND e_chk.reverses_event_id = e.id
      AND e_chk.enabled = TRUE
  ) AS was_reversed,
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
LEFT JOIN signals tsig
  ON tsig.id = e.triggered_by_signal_id AND tsig.enabled = TRUE
LEFT JOIN events e_rev
  ON e_rev.id = e.reverses_event_id AND e_rev.enabled = TRUE
WHERE e.enabled = TRUE;
