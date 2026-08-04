-- ====================================
-- user_view_preferences
-- Per-user per-scope display preferences (view mode, grouping,
-- density, column visibility, etc.) stored as JSON.
-- ====================================
CREATE TABLE user_view_preferences (
  id              INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id       BINARY(16) NOT NULL                    COMMENT 'UUID v7, the only externally visible ID',
  workspace_id    INT UNSIGNED NOT NULL,
  user_id         INT UNSIGNED NOT NULL,

  scope_type      ENUM('workspace','project','lens','timebox') NOT NULL,
  scope_public_id BINARY(16) NULL COMMENT 'NULL for workspace scope',
  prefs_json      JSON NOT NULL COMMENT 'view_mode, group_by, density, column_order, hidden_columns...',

  sort_weight     INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes           TEXT NULL COMMENT 'Admin notes',
  enabled         BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at      TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_user_view_prefs_public_id (public_id),
  UNIQUE KEY uniq_user_view_prefs_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_user_view_prefs_user_scope (user_id, scope_type, scope_public_id),
  KEY idx_user_view_prefs_workspace_user (workspace_id, user_id),

  CONSTRAINT fk_user_view_prefs_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_user_view_prefs_user      FOREIGN KEY (user_id)      REFERENCES users(id)      ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Per-user per-scope display preferences';
