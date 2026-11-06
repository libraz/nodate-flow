-- ====================================
-- task_review_decisions
-- One immutable decision per submission. A new decision requires a new
-- submission row, preserving the complete review history.
-- ====================================
CREATE TABLE task_review_decisions (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id',
  submission_id INT UNSIGNED NOT NULL COMMENT 'Submission this decision resolves',
  decided_by_user_id INT UNSIGNED NULL COMMENT 'Human reviewer who made the decision; nullable provenance if the account is removed',
  decision ENUM('approved','rejected') NOT NULL COMMENT 'Review outcome for the immutable submission',
  reason TEXT NULL COMMENT 'Optional reviewer explanation',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_task_review_decisions_public_id (public_id),
  UNIQUE KEY uniq_task_review_decisions_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_task_review_decisions_submission (submission_id),
  KEY idx_task_review_decisions_task_created (task_id, created_at),

  CONSTRAINT fk_task_review_decisions_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_review_decisions_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_review_decisions_submission FOREIGN KEY (submission_id) REFERENCES task_submissions(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_review_decisions_reviewer FOREIGN KEY (decided_by_user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Immutable human decisions for task submissions';
