-- ====================================
-- events
-- Append-only event log. All workspace state transitions flow through this
-- table. The API offers no DELETE endpoint; purgeWorkspace is the sole
-- deletion path (test fixtures only).
-- ====================================
CREATE TABLE events (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NULL COMMENT 'Internal FK to tasks.id when the event targets a task',
  actor_user_id INT UNSIGNED NULL COMMENT 'Acting user.id (null for system/bot actions)',

  type VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Event type (e.g., task.created, signal.attached)',
  payload_json JSON NOT NULL COMMENT 'Event payload',
  occurred_at DATETIME NOT NULL COMMENT 'Logical time of the event (second precision; ties broken by id)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_events_public_id (public_id),
  KEY idx_events_workspace_id_occurred_at (workspace_id, occurred_at),
  KEY idx_events_workspace_id_task_id_occurred_at (workspace_id, task_id, occurred_at),
  KEY idx_events_workspace_id_type (workspace_id, type),

  CONSTRAINT fk_events_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_events_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_events_actor FOREIGN KEY (actor_user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Append-only event log';
