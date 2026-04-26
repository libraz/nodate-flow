-- ====================================
-- task_description_versions
-- Task description version history (full snapshots).
-- Each edit creates a new row; the latest version_number is current.
-- ====================================
CREATE TABLE task_description_versions (
  id             INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id      BINARY(16) NOT NULL                    COMMENT 'UUID v7, the only externally visible ID',
  workspace_id   INT UNSIGNED NOT NULL                   COMMENT 'Internal FK to workspaces.id',
  task_id        INT UNSIGNED NOT NULL                   COMMENT 'Internal FK to tasks.id',
  author_user_id INT UNSIGNED NULL                       COMMENT 'User who authored this version',

  version_number INT UNSIGNED NOT NULL                   COMMENT 'Monotonically increasing per task',
  body           MEDIUMTEXT NOT NULL                     COMMENT 'Full description snapshot',
  body_length    INT UNSIGNED NOT NULL DEFAULT 0         COMMENT 'Char count (cached)',

  sort_weight    INT NOT NULL DEFAULT 0                  COMMENT 'Display order',
  notes          TEXT NULL                               COMMENT 'Admin notes',
  enabled        BOOLEAN NOT NULL DEFAULT TRUE           COMMENT 'Enabled flag',
  updated_at     TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at     DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_task_desc_versions_public_id (public_id),
  UNIQUE KEY uniq_task_desc_versions_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_task_desc_versions_task_version (task_id, version_number),
  KEY idx_task_desc_versions_workspace_task (workspace_id, task_id, created_at DESC),

  CONSTRAINT fk_task_desc_versions_workspace FOREIGN KEY (workspace_id)   REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_desc_versions_task      FOREIGN KEY (task_id)        REFERENCES tasks(id)      ON DELETE CASCADE,
  CONSTRAINT fk_task_desc_versions_author    FOREIGN KEY (author_user_id) REFERENCES users(id)      ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Task description version history (full snapshots)';
