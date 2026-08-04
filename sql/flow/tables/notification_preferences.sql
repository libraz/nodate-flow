-- ====================================
-- notification_preferences
-- Granular per-user notification preferences. Each row controls
-- one (event_category, channel) pair; is_muted suppresses delivery.
-- ====================================
CREATE TABLE notification_preferences (
  id             INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id      BINARY(16) NOT NULL                    COMMENT 'UUID v7, the only externally visible ID',
  workspace_id   INT UNSIGNED NOT NULL,
  user_id        INT UNSIGNED NOT NULL,

  event_category VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL
    COMMENT 'task.lifecycle, task.comment, task.mention, relation, timebox, ai, etc.',
  channel        ENUM('in_app','email','push') NOT NULL,
  is_muted       BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'TRUE = suppress this category+channel',

  sort_weight    INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes          TEXT NULL COMMENT 'Admin notes',
  enabled        BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at     TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at     DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_notif_prefs_public_id (public_id),
  UNIQUE KEY uniq_notif_prefs_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_notif_prefs_user_ws_cat_chan (user_id, workspace_id, event_category, channel),
  KEY idx_notif_prefs_workspace_user (workspace_id, user_id),

  CONSTRAINT fk_notif_prefs_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_notif_prefs_user      FOREIGN KEY (user_id)      REFERENCES users(id)      ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Granular per-user notification preferences';
