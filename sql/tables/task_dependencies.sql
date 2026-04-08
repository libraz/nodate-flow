-- ====================================
-- task_dependencies
-- Directed edges between tasks. from_task blocks/relates to to_task.
-- ====================================
CREATE TABLE task_dependencies (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  from_task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id (source)',
  to_task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id (target)',

  kind ENUM('blocks','relates','duplicates','subtask_of') NOT NULL DEFAULT 'blocks' COMMENT 'Dependency kind',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_task_dependencies_public_id (public_id),
  UNIQUE KEY uniq_task_dependencies_edge (from_task_id, to_task_id, kind),
  KEY idx_task_dependencies_workspace_id_to_task_id (workspace_id, to_task_id),

  CONSTRAINT fk_task_dependencies_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_dependencies_from FOREIGN KEY (from_task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_dependencies_to FOREIGN KEY (to_task_id) REFERENCES tasks(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Directed task dependencies';
