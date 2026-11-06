-- ====================================
-- task_handoffs
-- Human responsibility moves only when the recipient accepts. The
-- generated pending_role_key scopes a unique key to live handoffs while
-- allowing unlimited immutable history after a handoff is resolved.
-- ====================================
CREATE TABLE task_handoffs (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id',
  role ENUM('assignee','reviewer','approver') NOT NULL COMMENT 'Human responsibility role being handed off',
  from_user_id INT UNSIGNED NULL COMMENT 'Current human responsibility holder until acceptance',
  to_user_id INT UNSIGNED NULL COMMENT 'Eligible task reader who may accept or decline',
  requested_by_user_id INT UNSIGNED NULL COMMENT 'Human who requested the handoff',
  due_at DATETIME(3) NULL COMMENT 'Optional response deadline; stored in UTC',
  status ENUM('pending','accepted','declined','expired','cancelled') NOT NULL DEFAULT 'pending' COMMENT 'Only acceptance changes the task actor',
  pending_role_key VARCHAR(16) GENERATED ALWAYS AS (CASE WHEN status = 'pending' THEN role ELSE NULL END) STORED COMMENT 'Scopes the unique key to one unresolved handoff per task role',
  resolved_at DATETIME(3) NULL COMMENT 'Time the handoff was accepted, declined, cancelled, or marked expired',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_task_handoffs_public_id (public_id),
  UNIQUE KEY uniq_task_handoffs_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_task_handoffs_one_pending_role (task_id, pending_role_key),
  KEY idx_task_handoffs_recipient (workspace_id, to_user_id, status, created_at),
  KEY idx_task_handoffs_task_created (task_id, created_at),
  KEY idx_task_handoffs_status_due_at (status, due_at, id),

  CONSTRAINT fk_task_handoffs_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_handoffs_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_handoffs_from_user FOREIGN KEY (from_user_id) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_task_handoffs_to_user FOREIGN KEY (to_user_id) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_task_handoffs_requested_by FOREIGN KEY (requested_by_user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Accepted human task responsibility handoffs';
