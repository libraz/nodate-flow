-- ====================================
-- comments
-- Discussion thread attached to a task. Markdown body with edit history
-- captured via edited_at (full revisions out of scope).
-- ====================================
CREATE TABLE comments (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id',
  author_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id',

  body MEDIUMTEXT NOT NULL COMMENT 'Markdown body',
  edited_at DATETIME(3) NULL COMMENT 'Last edit time (null = never edited)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_comments_public_id (public_id),
  UNIQUE KEY uniq_comments_workspace_public_id (workspace_id, public_id),
  KEY idx_comments_workspace_id_task_id (workspace_id, task_id),
  KEY idx_comments_workspace_id_author_id (workspace_id, author_id),
  -- Supports keyset pagination on (created_at DESC, public_id DESC) for
  -- ListCommentsForTaskKeyset.
  KEY idx_comments_task_id_keyset (task_id, created_at, public_id),
  FULLTEXT KEY ft_comments_body (body),

  CONSTRAINT fk_comments_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_comments_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_comments_author FOREIGN KEY (author_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Task discussion comments';
