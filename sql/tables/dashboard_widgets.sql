-- ====================================
-- dashboard_widgets
-- Configurable widgets that users arrange on their workspace dashboard.
-- Each widget has a type, grid position, and JSON configuration blob.
-- ====================================
CREATE TABLE dashboard_widgets (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  creator_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id',

  widget_type ENUM('task_summary','burndown','signals_feed','ai_suggestions','overdue_tasks','notification_feed') NOT NULL COMMENT 'Widget variant',
  title VARCHAR(200) NOT NULL COMMENT 'User-facing widget title',
  config JSON NULL COMMENT 'Widget-specific configuration (filters, timebox_id, etc.)',
  position_x SMALLINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Grid column offset',
  position_y SMALLINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Grid row offset',
  width SMALLINT UNSIGNED NOT NULL DEFAULT 1 COMMENT 'Grid column span',
  height SMALLINT UNSIGNED NOT NULL DEFAULT 1 COMMENT 'Grid row span',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_dashboard_widgets_public_id (public_id),
  KEY idx_dashboard_widgets_workspace_id_enabled_sort_weight (workspace_id, enabled, sort_weight),
  KEY idx_dashboard_widgets_workspace_id_creator_id (workspace_id, creator_id),

  CONSTRAINT fk_dashboard_widgets_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_dashboard_widgets_creator FOREIGN KEY (creator_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Dashboard widgets arranged by users';
