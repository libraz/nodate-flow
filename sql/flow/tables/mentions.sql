-- ====================================
-- mentions
-- @mention cache extracted from markdown in task descriptions
-- and comments. Can be re-extracted; serves as a fast lookup
-- for notification fan-out.
-- ====================================
CREATE TABLE mentions (
  id                INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id         BINARY(16) NOT NULL                    COMMENT 'UUID v7, the only externally visible ID',
  workspace_id      INT UNSIGNED NOT NULL,
  mentioned_user_id INT UNSIGNED NOT NULL,
  actor_user_id     INT UNSIGNED NULL,

  task_id           INT UNSIGNED NULL,
  comment_id        INT UNSIGNED NULL,
  source            ENUM('task_description','comment') NOT NULL,

  sort_weight       INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes             TEXT NULL COMMENT 'Admin notes',
  enabled           BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at        TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_mentions_public_id (public_id),
  UNIQUE KEY uniq_mentions_workspace_public_id (workspace_id, public_id),
  KEY idx_mentions_workspace_mentioned_user (workspace_id, mentioned_user_id),
  KEY idx_mentions_task_id (task_id),
  KEY idx_mentions_comment_id (comment_id),

  CONSTRAINT fk_mentions_workspace FOREIGN KEY (workspace_id)      REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_mentions_mentioned FOREIGN KEY (mentioned_user_id) REFERENCES users(id)      ON DELETE CASCADE,
  CONSTRAINT fk_mentions_actor     FOREIGN KEY (actor_user_id)     REFERENCES users(id)      ON DELETE SET NULL,
  CONSTRAINT fk_mentions_task      FOREIGN KEY (task_id)           REFERENCES tasks(id)      ON DELETE CASCADE,
  CONSTRAINT fk_mentions_comment   FOREIGN KEY (comment_id)        REFERENCES comments(id)   ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='@mention cache (re-extractable from markdown)';
