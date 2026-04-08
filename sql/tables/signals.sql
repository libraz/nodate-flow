-- ====================================
-- signals
-- External or manually submitted signals (webhooks, manual drops) that may
-- be normalized and attached to a task. Optionally linked to a task.
-- ====================================
CREATE TABLE signals (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NULL COMMENT 'Internal FK to tasks.id, if resolved',

  source ENUM('manual','github','slack','email','webhook') NOT NULL COMMENT 'Originating channel',
  kind VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Source-specific event kind (e.g., pull_request.opened)',
  external_id VARCHAR(255) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'External identifier (delivery id, message ts, ...)',
  payload_json JSON NOT NULL COMMENT 'Raw normalized payload',
  received_at DATETIME NOT NULL COMMENT 'Time the signal was received',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_signals_public_id (public_id),
  KEY idx_signals_workspace_id_received_at (workspace_id, received_at),
  KEY idx_signals_workspace_id_task_id (workspace_id, task_id),
  KEY idx_signals_source_external_id (source, external_id),

  CONSTRAINT fk_signals_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_signals_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Inbound signals';
