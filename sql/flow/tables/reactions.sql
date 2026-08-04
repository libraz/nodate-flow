-- ====================================
-- reactions
-- Emoji reactions on tasks and comments. Exactly one of task_id
-- or comment_id must be non-NULL (enforced by CHECK constraint).
-- ====================================
CREATE TABLE reactions (
  id           INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id    BINARY(16) NOT NULL                    COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL,
  user_id      INT UNSIGNED NOT NULL,

  task_id      INT UNSIGNED NULL,
  comment_id   INT UNSIGNED NULL,
  emoji        VARCHAR(32) NOT NULL COMMENT 'Unicode emoji',

  sort_weight  INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes        TEXT NULL COMMENT 'Admin notes',
  enabled      BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at   TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at   DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_reactions_public_id (public_id),
  UNIQUE KEY uniq_reactions_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_reactions_user_task_emoji (user_id, task_id, emoji, enabled),
  UNIQUE KEY uniq_reactions_user_comment_emoji (user_id, comment_id, emoji, enabled),
  KEY idx_reactions_task_id_emoji (task_id, emoji),
  KEY idx_reactions_comment_id_emoji (comment_id, emoji),

  CONSTRAINT fk_reactions_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_reactions_user      FOREIGN KEY (user_id)      REFERENCES users(id)      ON DELETE CASCADE,
  CONSTRAINT fk_reactions_task      FOREIGN KEY (task_id)      REFERENCES tasks(id)      ON DELETE CASCADE,
  CONSTRAINT fk_reactions_comment   FOREIGN KEY (comment_id)   REFERENCES comments(id)   ON DELETE CASCADE,

  CONSTRAINT chk_reactions_target CHECK (
    (task_id IS NOT NULL AND comment_id IS NULL) OR
    (comment_id IS NOT NULL AND task_id IS NULL)
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Emoji reactions on tasks and comments';
