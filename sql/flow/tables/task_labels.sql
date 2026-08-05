-- ====================================
-- task_labels
-- Junction table linking tasks to labels.
-- ====================================
CREATE TABLE task_labels (
  id           INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id    BINARY(16) NOT NULL                    COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL                   COMMENT 'Internal FK to workspaces.id',
  task_id      INT UNSIGNED NOT NULL                   COMMENT 'Internal FK to tasks.id',
  label_id     INT UNSIGNED NOT NULL                   COMMENT 'Internal FK to labels.id',

  sort_weight  INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes        TEXT NULL COMMENT 'Admin notes',
  enabled      BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  -- Liveness marker scoping the unique key below to live rows: 1 while
  -- enabled, NULL once soft-deleted, so tombstones leave the index
  -- rather than colliding with each other. See the soft-delete rule in
  -- sql/core/conformance/schema/40-soft-delete-uniqueness.sql.
  active TINYINT UNSIGNED GENERATED ALWAYS AS (IF(enabled, 1, NULL)) VIRTUAL COMMENT 'NULL once soft-deleted; exists only to scope the unique key below to live rows',
  updated_at   TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at   DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_task_labels_public_id (public_id),
  UNIQUE KEY uniq_task_labels_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_task_labels_task_id_label_id_active (task_id, label_id, active),
  KEY idx_task_labels_workspace_id_label_id (workspace_id, label_id),

  CONSTRAINT fk_task_labels_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_labels_task      FOREIGN KEY (task_id)      REFERENCES tasks(id)      ON DELETE CASCADE,
  CONSTRAINT fk_task_labels_label     FOREIGN KEY (label_id)     REFERENCES labels(id)     ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Task-label junction';
