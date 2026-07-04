-- name: AppendEvent :execlastid
-- Append a single user-actor event to the append-only event log. The events
-- table has no UPDATE/DELETE path; only purgeWorkspace removes rows.
--
-- `actor_system_source` is bound here so the third actor source (ADR 0008
-- D8) can be expressed without a separate INSERT path; callers pass an
-- empty string when the row is genuinely user-attributed. The three-way
-- actor exclusion (actor_user_id / actor_agent_id / actor_system_source)
-- is enforced by handler-side validation (eventbus.validateActors) rather
-- than a CHECK constraint because MySQL 8.4 forbids CHECK constraints on
-- columns used in FK ON DELETE SET NULL actions; see sql/tables/events.sql.
--
-- `triggered_by_signal_id` (ADR 0008 D4) is the internal signal id when the
-- event was emitted by the Applier in response to a judged signal. NULL for
-- events with no signal lineage. ON DELETE SET NULL on the FK so signal
-- purge does not cascade to the event log.
--
-- `reverses_event_id` (ADR 0008 D4 / J5) is the internal event id this row
-- compensates. NULL for non-reversal events. The reversal handler binds
-- this when appending a same-type compensating event so the derived_state
-- projection can cancel the original out without ever UPDATEing the event
-- log (events stay immutable; see CLAUDE.md rule 10). ON DELETE SET NULL
-- so purging a reversed event detaches the backlink instead of cascading.
INSERT INTO events (
  public_id,
  workspace_id,
  task_id,
  actor_user_id,
  actor_system_source,
  triggered_by_signal_id,
  reverses_event_id,
  type,
  payload_json,
  occurred_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: AppendAgentEvent :execlastid
-- Append a single agent-actor event to the append-only event log. Mirrors
-- AppendEvent but binds actor_agent_id and leaves actor_user_id NULL,
-- preserving the actor_user_id / actor_agent_id mutual exclusion that the
-- events table relies on (enforced by query design rather than a CHECK
-- constraint; see sql/tables/events.sql for the rationale). Used by the
-- orchestrator runner when emitting ai.agent.run.* events and by MCP tool
-- calls dispatched from an agent context.
--
-- `triggered_by_signal_id` (ADR 0008 D4) is set when the Applier emits this
-- event in response to a judged signal so the timeline UI can render the
-- causal chain "signal -> judge verdict -> task event".
--
-- `reverses_event_id` (ADR 0008 D4 / J5) mirrors the same field on
-- AppendEvent. The reversal handler uses this INSERT path when the target
-- event being reversed was originally produced by an agent so the
-- compensating event preserves the same actor kind. NULL for normal
-- agent-emitted events.
INSERT INTO events (
  public_id,
  workspace_id,
  task_id,
  actor_agent_id,
  triggered_by_signal_id,
  reverses_event_id,
  type,
  payload_json,
  occurred_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListEventsForTask :many
-- List a task's timeline via v_task_timeline. Projects
-- `actor_system_source` (ADR 0008 D8, third actor source for worker-tick
-- events), `triggered_by_signal_public_id` (ADR 0008 D4, signal
-- traceability link), `reverses_event_public_id` (ADR 0008 D4 / J5,
-- target event the row compensates) and `was_reversed` (TRUE when some
-- other enabled event reverses this one) so the timeline UI can render
-- the causal chain plus the reversal state of each entry. The view
-- backs `was_reversed` with a correlated EXISTS on idx_events_reverses
-- so per-row cost stays bounded.
SELECT
  v.public_id,
  v.task_public_id,
  v.actor_user_public_id,
  v.actor_display_name,
  v.actor_system_source,
  v.triggered_by_signal_public_id,
  v.reverses_event_public_id,
  v.was_reversed,
  v.type,
  v.payload_json,
  v.occurred_at,
  COUNT(*) OVER() AS total
FROM v_task_timeline v
WHERE v.workspace_id = ?
  AND v.task_public_id = ?
ORDER BY v.occurred_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ListTransitionEventsForReplay :many
-- Ordered list of task.transition.* events for a single task,
-- ascending by occurred_at + id. Used by the replay tool to derive
-- the expected derived_state from scratch.
SELECT id, type, occurred_at, reverses_event_id
FROM events
WHERE workspace_id = ?
  AND task_id = ?
  AND type LIKE 'task.transition.%'
ORDER BY occurred_at ASC, id ASC;

-- name: ListEventsForProject :many
-- List a project's timeline via v_task_timeline. Filters events whose
-- owning task lives in the given project (events with no task_id are
-- excluded by virtue of project_public_id being NULL). Projects
-- `actor_system_source`, `triggered_by_signal_public_id`,
-- `reverses_event_public_id` and `was_reversed` for the same reasons as
-- ListEventsForTask.
SELECT
  v.public_id,
  v.task_public_id,
  v.project_public_id,
  v.actor_user_public_id,
  v.actor_display_name,
  v.actor_system_source,
  v.triggered_by_signal_public_id,
  v.reverses_event_public_id,
  v.was_reversed,
  v.type,
  v.payload_json,
  v.occurred_at,
  COUNT(*) OVER() AS total
FROM v_task_timeline v
WHERE v.workspace_id = ?
  AND v.project_public_id = ?
ORDER BY v.occurred_at DESC, v.event_id DESC
LIMIT ? OFFSET ?;

-- name: ListPendingAiSuggestions :many
-- List pending AI suggestions for a workspace. A suggestion is "pending"
-- when an ai.suggestion.proposed event exists with no later
-- ai.suggestion.applied / ai.suggestion.dismissed event for the same
-- inbox_item_id (compared via JSON_EXTRACT on payload_json).
SELECT
  e.public_id,
  e.occurred_at,
  e.payload_json
FROM events e
WHERE e.workspace_id = ?
  AND e.type = 'ai.suggestion.proposed'
  AND NOT EXISTS (
    SELECT 1 FROM events e2
    WHERE e2.workspace_id = e.workspace_id
      AND e2.type IN ('ai.suggestion.applied', 'ai.suggestion.dismissed')
      AND e2.id > e.id
      AND JSON_EXTRACT(e2.payload_json, '$.inbox_item_id') = JSON_EXTRACT(e.payload_json, '$.inbox_item_id')
  )
ORDER BY e.occurred_at DESC
LIMIT 100;

-- name: CountAiSuggestionOutcomesForWorkspace :one
-- Count ai.suggestion.{proposed,applied,dismissed} events for a workspace
-- within the given time window. Used by the AI metrics endpoint
-- to compute acceptance rate.
SELECT
  COALESCE(SUM(CASE WHEN type = 'ai.suggestion.proposed'  THEN 1 ELSE 0 END), 0) AS proposed,
  COALESCE(SUM(CASE WHEN type = 'ai.suggestion.applied'   THEN 1 ELSE 0 END), 0) AS applied,
  COALESCE(SUM(CASE WHEN type = 'ai.suggestion.dismissed' THEN 1 ELSE 0 END), 0) AS dismissed
FROM events
WHERE workspace_id = ?
  AND occurred_at >= ?
  AND type IN ('ai.suggestion.proposed', 'ai.suggestion.applied', 'ai.suggestion.dismissed');

-- name: ListEventsForWorkspace :many
-- List the workspace-wide event timeline via v_task_timeline. Projects
-- `actor_system_source`, `triggered_by_signal_public_id`,
-- `reverses_event_public_id` and `was_reversed` for the same reasons as
-- ListEventsForTask.
SELECT
  v.public_id,
  v.task_public_id,
  v.actor_user_public_id,
  v.actor_display_name,
  v.actor_system_source,
  v.triggered_by_signal_public_id,
  v.reverses_event_public_id,
  v.was_reversed,
  v.type,
  v.payload_json,
  v.occurred_at,
  COUNT(*) OVER() AS total
FROM v_task_timeline v
WHERE v.workspace_id = ?
ORDER BY v.occurred_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: HasRecentEventsForWorkspace :one
-- Check if any events have occurred in the workspace since the given timestamp.
-- Used by the agent runtime pre-flight check to skip LLM calls when idle.
SELECT EXISTS(
  SELECT 1 FROM events
  WHERE workspace_id = ? AND occurred_at > ?
  LIMIT 1
) AS has_events;

-- name: GetEventPublicIDAndOccurredAt :one
-- Resolve an event's public id and logical occurrence time given its
-- internal id, scoped by workspace as a defence-in-depth check.
-- Used by the webhook fanout chain (H1): the worker needs the event's
-- public_id to populate the dedupe key and the row's occurred_at to set
-- the webhook OccurredAt field, instead of using time.Now() which would
-- attribute the wrong instant when delivery is retried.
-- occurred_at (not created_at) is the contract because it is the logical
-- event time set by the eventbus producer; created_at is just the row
-- insertion time and could drift from the event's true occurrence.
SELECT public_id, occurred_at
FROM events
WHERE workspace_id = ?
  AND id = ?
LIMIT 1;

-- name: FindEventForReverse :one
-- Resolve a target event by public_id for the reversal handler (ADR 0008
-- D4 / J5, POST /workspaces/{wsId}/events/{eventPublicId}/reverse). The
-- handler uses this single query to drive three eligibility checks
-- before appending the compensating event:
--   - target exists in the caller's workspace (LIMIT 1 + workspace_id =
--     ? gives 404 vs. 403 separation when combined with the standard
--     ACL middleware).
--   - target is LLM-origin (actor_agent_id IS NOT NULL); user-actor and
--     system-source rows are rejected with AI_REVERSE_NOT_LLM_ORIGIN.
--   - target has not already been reversed (was_reversed = FALSE);
--     double-reverse is rejected with AI_REVERSE_ALREADY_REVERSED.
-- The `was_reversed` EXISTS subquery uses alias `e_chk` (reverse check),
-- matching the v_task_timeline view, and is index-covered by
-- idx_events_reverses (workspace_id, reverses_event_id).
SELECT
  e.id,
  e.workspace_id,
  e.type,
  e.actor_user_id,
  e.actor_agent_id,
  e.actor_system_source,
  e.triggered_by_signal_id,
  e.reverses_event_id,
  EXISTS (
    SELECT 1 FROM events e_chk
    WHERE e_chk.workspace_id = e.workspace_id
      AND e_chk.reverses_event_id = e.id
      AND e_chk.enabled = TRUE
  ) AS was_reversed
FROM events e
WHERE e.public_id = ?
  AND e.workspace_id = ?
  AND e.enabled = TRUE
LIMIT 1;
