-- ====================================
-- user_favorites
-- Per-user starred entities (projects, tasks, pages, lenses, timeboxes).
-- ====================================
CREATE TABLE user_favorites (
  id               INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id        BINARY(16) NOT NULL                    COMMENT 'UUID v7, the only externally visible ID',
  workspace_id     INT UNSIGNED NOT NULL,
  user_id          INT UNSIGNED NOT NULL,

  target_type      ENUM('project','task','page','lens','timebox') NOT NULL,
  target_public_id BINARY(16) NOT NULL COMMENT 'public_id of the favorited entity',
  folder_name      VARCHAR(64) NULL COMMENT 'Optional grouping folder',

  sort_weight      INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes            TEXT NULL COMMENT 'Admin notes',
  enabled          BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_user_favorites_public_id (public_id),
  UNIQUE KEY uniq_user_favorites_user_target (user_id, target_type, target_public_id, enabled),
  KEY idx_user_favorites_workspace_user (workspace_id, user_id),

  CONSTRAINT fk_user_favorites_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_user_favorites_user      FOREIGN KEY (user_id)      REFERENCES users(id)      ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Per-user starred entities';
