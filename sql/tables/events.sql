-- ====================================
-- events
-- Append-only event log. All workspace state transitions flow through this
-- table. The API offers no DELETE endpoint; purgeWorkspace is the sole
-- deletion path (test fixtures only).
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
  actor_user_id INT UNSIGNED NULL COMMENT 'Acting user.id (null for system/bot actions)',
  actor_agent_id INT UNSIGNED NULL COMMENT 'Acting ai_agents.id when the event was produced by an AI agent. Mutual exclusion with actor_user_id is enforced by query design and handler validation, not a CHECK constraint (MySQL 8.4 forbids CHECK on columns used by FK referential actions; both FKs use ON DELETE SET NULL). Each INSERT binds exactly one of the two columns: AppendEvent (events.sql) sets actor_user_id only; AppendAgentEvent (events.sql) and InsertHandoffToUserEvent (agents/handoff.sql) set actor_agent_id only; InsertHandoffToAgentEvent (agents/handoff.sql) sets actor_user_id only. Both NULL means system actor.',

  type VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Event type (e.g., task.created, signal.attached)',
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

  CONSTRAINT fk_events_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_events_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_events_actor FOREIGN KEY (actor_user_id) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_events_actor_agent FOREIGN KEY (actor_agent_id) REFERENCES ai_agents(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Append-only event log';
