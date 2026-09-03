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
--
-- Projects `actor_system_source` (ADR 0008 D8) and the LEFT JOINed
-- `triggered_by_signal_public_id` (ADR 0008 D4) so the agent-runs handler
-- can surface the third actor source and the signal causal link without
-- exposing internal ids on the API boundary (CLAUDE.md rule 18). The
-- signal join uses alias `tsig` (triggering signal) to mirror the
-- v_task_timeline view.
--
-- Reversal projection (ADR 0008 D4): `reverses_event_public_id`
-- comes from the LEFT self-join aliased `e_rev` (reverse target);
-- `was_reversed` is computed by a correlated EXISTS subquery using
-- alias `e_chk` (reverse check), backed by idx_events_reverses
-- (workspace_id, reverses_event_id) so the per-row scan stays bounded.
SELECT
  e.public_id,
  e.task_id,
  e.actor_agent_id,
  tsig.public_id AS triggered_by_signal_public_id,
  e_rev.public_id AS reverses_event_public_id,
  EXISTS (
    SELECT 1 FROM events e_chk
    WHERE e_chk.workspace_id = e.workspace_id
      AND e_chk.reverses_event_id = e.id
      AND e_chk.enabled = TRUE
  ) AS was_reversed,
  e.actor_system_source,
  a.public_id AS agent_public_id,
  a.name      AS agent_name,
  e.type,
  e.payload_json,
  e.occurred_at,
  COUNT(*) OVER() AS total
FROM events e
LEFT JOIN ai_agents a
  ON a.id = e.actor_agent_id AND a.enabled = TRUE
LEFT JOIN signals tsig
  ON tsig.id = e.triggered_by_signal_id AND tsig.enabled = TRUE
LEFT JOIN events e_rev
  ON e_rev.id = e.reverses_event_id AND e_rev.enabled = TRUE
WHERE e.workspace_id = ?
  AND e.task_id = ?
  AND e.actor_agent_id IS NOT NULL
  AND e.type LIKE 'ai.agent.run.%'
ORDER BY e.occurred_at DESC, e.id DESC
LIMIT ?;
