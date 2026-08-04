-- ====================================
-- events
-- Append-only event log. All workspace state transitions flow through this
-- table. The API offers no DELETE endpoint; purgeWorkspace is the sole
-- deletion path (test fixtures only).
--
-- See ADR 0008 (docs/adr/0008-signals-and-judge-loop.md) D4 for the
-- `triggered_by_signal_id` traceability link and D8 for the third actor
-- source `actor_system_source` (worker-tick events).
-- ====================================
CREATE TABLE events (
  -- BIGINT UNSIGNED is a deliberate exception to the project default
  -- (INT UNSIGNED for IDs). Justification: this is an append-only event log
  -- expected to grow indefinitely; the ~4.29B INT UNSIGNED ceiling is
  -- reachable in long-lived deployments. Any FK that targets this column
  -- (currently notifications.source_event_id) must match the type.
  id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed; BIGINT UNSIGNED for unbounded append-only growth',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NULL COMMENT 'Internal FK to tasks.id when the event targets a task',
  triggered_by_signal_id INT UNSIGNED NULL COMMENT 'Internal FK to signals.id; set when this event was emitted by the Applier in response to a judged signal. Provides full traceability from external input to task event (ADR 0008 D4).',
  actor_user_id INT UNSIGNED NULL COMMENT 'Acting user.id (null for system/bot actions). Mutually exclusive with actor_agent_id and actor_system_source: exactly one of the three actor sources is set per row (both NULL is also legal for legacy "system actor"). The mutual-exclusion rule is enforced by query design and handler validation, not a CHECK constraint, because all three FK referential actions use ON DELETE SET NULL and MySQL 8.4 forbids CHECK constraints referencing columns used in FK referential actions. Each INSERT binds exactly one of the three columns: AppendEvent (events.sql) sets actor_user_id only; AppendAgentEvent (events.sql) and InsertHandoffToUserEvent (agents/handoff.sql) set actor_agent_id only; InsertHandoffToAgentEvent (agents/handoff.sql) sets actor_user_id only; worker-tick append paths set actor_system_source only.',
  actor_agent_id INT UNSIGNED NULL COMMENT 'Acting ai_agents.id when the event was produced by an AI agent (judge / task agent). See actor_user_id comment for the three-way exclusion rule.',
  actor_system_source VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'Third actor source for system-driven events emitted by the worker binary (apps/flow-worker; ADR 0008 D8). Examples: `worker:scheduler`, `worker:retention`, `worker:calendar`. Not an FK because the worker is not represented in the database. See actor_user_id comment for the three-way exclusion rule.',
  reverses_event_id BIGINT UNSIGNED NULL COMMENT 'Internal FK to events.id. Non-NULL means this event is a compensating reverse of another event (e.g., user undoing an auto-completion). The derived_state projection cancels both events out. See ADR 0008 D4 — events are immutable; reversals never UPDATE/DELETE.',

  type VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Event type (e.g., task.created, signal.attached, signal.judged)',
  payload_json JSON NOT NULL CHECK (JSON_VALID(payload_json)) COMMENT 'Event payload',
  occurred_at DATETIME(3) NOT NULL COMMENT 'Logical time of the event (millisecond precision; ties broken by id)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_events_public_id (public_id),
  UNIQUE KEY uniq_events_workspace_public_id (workspace_id, public_id),
  KEY idx_events_workspace_id_occurred_at (workspace_id, occurred_at),
  KEY idx_events_workspace_id_task_id_occurred_at (workspace_id, task_id, occurred_at),
  KEY idx_events_workspace_id_type (workspace_id, type),
  KEY idx_events_workspace_id_actor_agent_id (workspace_id, actor_agent_id),
  KEY idx_events_triggered_by_signal (triggered_by_signal_id),
  -- UNIQUE guards against duplicate compensating reversals: two concurrent
  -- reverses of the same event would otherwise both insert a compensating
  -- row and double-cancel in the derived_state projection. MySQL treats
  -- multiple NULLs as distinct, so ordinary (non-reverse) events with
  -- reverses_event_id IS NULL are unaffected; only genuine reverses dedupe.
  UNIQUE KEY uniq_events_reverses (workspace_id, reverses_event_id),

  -- fk_events_task, fk_events_actor_agent and fk_events_triggered_by_signal
  -- are NOT declared here. Their columns are core, so every writer produces
  -- rows of the same shape, but `tasks`, `ai_agents` and `signals` belong to
  -- the flow layer. Those three constraints are added by
  -- sql/flow/constraints/ when that layer is present; in a core-only
  -- deployment the columns are always NULL.
  CONSTRAINT fk_events_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_events_actor FOREIGN KEY (actor_user_id) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_events_reverses FOREIGN KEY (reverses_event_id) REFERENCES events(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Append-only event log';
