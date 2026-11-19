-- ====================================
-- workspace_embedding_settings
-- Per-workspace permission to send task text to the instance embedding provider.
-- A missing row has the same meaning as mode='inherit'.
-- ====================================
CREATE TABLE workspace_embedding_settings (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Workspace this embedding policy applies to',
  modified_by_user_id INT UNSIGNED NULL COMMENT 'Last modifying user.id; NULL when the user is removed',

  mode ENUM('inherit', 'disabled') NOT NULL DEFAULT 'inherit' COMMENT 'Whether to use the instance embedding provider or disable it for this workspace',

  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_workspace_embedding_settings_workspace (workspace_id),
  KEY idx_workspace_embedding_settings_modified_by_user_id (modified_by_user_id),

  CONSTRAINT fk_workspace_embedding_settings_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_workspace_embedding_settings_modifier FOREIGN KEY (modified_by_user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Per-workspace embedding provider policy';
