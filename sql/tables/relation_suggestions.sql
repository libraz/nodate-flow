-- ====================================
-- relation_suggestions
-- AI-generated suggestions for task relations (blocks, relates, duplicates).
-- Created when an embedding pipeline detects similarity between tasks.
-- ====================================
CREATE TABLE relation_suggestions (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  source_task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id (trigger task)',
  target_task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id (suggested relation)',

  suggested_kind ENUM('blocks','relates','duplicates') NOT NULL COMMENT 'Suggested dependency kind',
  confidence DECIMAL(5,4) NOT NULL COMMENT 'Cosine similarity score (0.0000 to 1.0000)',
  status ENUM('pending','accepted','dismissed') NOT NULL DEFAULT 'pending' COMMENT 'Resolution status',
  resolved_by INT UNSIGNED NULL COMMENT 'Internal FK to users.id (who resolved)',
  resolved_at DATETIME NULL COMMENT 'When the suggestion was accepted or dismissed',

  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_relation_suggestions_public_id (public_id),
  UNIQUE KEY uniq_relation_suggestions_edge (source_task_id, target_task_id, suggested_kind),
  KEY idx_relation_suggestions_workspace_id_status_created_at (workspace_id, status, created_at DESC),
  KEY idx_relation_suggestions_target_task_id_status (target_task_id, status),

  CONSTRAINT fk_relation_suggestions_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_relation_suggestions_source FOREIGN KEY (source_task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_relation_suggestions_target FOREIGN KEY (target_task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_relation_suggestions_resolved_by FOREIGN KEY (resolved_by) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT chk_relation_suggestions_no_self CHECK (source_task_id != target_task_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='AI-suggested task relation candidates';
