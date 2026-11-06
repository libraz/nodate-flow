-- ====================================
-- task_submissions
-- Immutable snapshots of submitted task work. A resubmission is a new
-- row, even when no artifact changed, so each review decision names one
-- precise evidence set and one work revision.
-- ====================================
CREATE TABLE task_submissions (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id',
  work_revision INT UNSIGNED NOT NULL COMMENT 'Task work_revision captured by this submission',
  submitted_by_user_id INT UNSIGNED NULL COMMENT 'Human who submitted the work; retained as nullable provenance if the account is removed',
  reviewer_user_id INT UNSIGNED NULL COMMENT 'Selected reviewer who may decide this submission',
  acceptance_criteria_json JSON NOT NULL COMMENT 'Immutable acceptance criteria supplied with the submission',
  evidence_urls_json JSON NOT NULL DEFAULT (JSON_ARRAY()) COMMENT 'Bounded HTTP(S) evidence links supplied with the submission',
  attachment_digests_json JSON NOT NULL DEFAULT (JSON_ARRAY()) COMMENT 'Attachment public IDs and SHA-256 digests snapshotted at submission time',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_task_submissions_public_id (public_id),
  UNIQUE KEY uniq_task_submissions_workspace_public_id (workspace_id, public_id),
  KEY idx_task_submissions_task_revision (task_id, work_revision, id),
  KEY idx_task_submissions_reviewer (workspace_id, reviewer_user_id, created_at),

  CONSTRAINT fk_task_submissions_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_submissions_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_submissions_submitter FOREIGN KEY (submitted_by_user_id) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_task_submissions_reviewer FOREIGN KEY (reviewer_user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Immutable task work submissions for human review';
