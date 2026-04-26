-- ====================================
-- mcp_invocations
-- Audit record for every MCP tool invocation. Arguments and results are
-- stored post-redaction (no raw secrets).
-- ====================================
CREATE TABLE mcp_invocations (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  user_id INT UNSIGNED NULL COMMENT 'Internal FK to users.id (PAT owner)',
  task_id INT UNSIGNED NULL COMMENT 'Internal FK to tasks.id when applicable',

  tool_name VARCHAR(128) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'MCP tool name (e.g., create_task)',
  arguments_redacted_json JSON NOT NULL COMMENT 'Redacted arguments',
  result_redacted_json JSON NULL COMMENT 'Redacted result payload',
  status ENUM('ok','error','denied') NOT NULL COMMENT 'Outcome',
  error_code VARCHAR(128) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'Error code when status != ok',
  duration_ms INT UNSIGNED NULL COMMENT 'Wall-clock duration in milliseconds',
  invoked_at DATETIME(3) NOT NULL COMMENT 'Invocation start time (millisecond precision)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_mcp_invocations_public_id (public_id),
  UNIQUE KEY uniq_mcp_invocations_workspace_public_id (workspace_id, public_id),
  KEY idx_mcp_invocations_workspace_id_invoked_at (workspace_id, invoked_at),
  KEY idx_mcp_invocations_workspace_id_user_id (workspace_id, user_id),
  KEY idx_mcp_invocations_created_at (created_at),

  CONSTRAINT fk_mcp_invocations_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_mcp_invocations_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_mcp_invocations_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='MCP tool invocation audit';
