-- ====================================
-- task_constraints
-- Declarative conditions attached to a task. The constraint engine
-- evaluates these in combination with events to derive task state.
-- ====================================
CREATE TABLE task_constraints (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id',

  kind ENUM('deadline','dependency','approval','signal','custom') NOT NULL COMMENT 'Constraint kind',
  expression TEXT NOT NULL COMMENT 'Engine-specific expression (JSON-ish DSL)',
  satisfied_at DATETIME(3) NULL COMMENT 'Time the constraint was first satisfied',
  failed_at DATETIME(3) NULL COMMENT 'Time the constraint was last evaluated as failing',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_task_constraints_public_id (public_id),
  UNIQUE KEY uniq_task_constraints_workspace_public_id (workspace_id, public_id),
  KEY idx_task_constraints_workspace_id_task_id (workspace_id, task_id),
  KEY idx_task_constraints_task_id_enabled (task_id, enabled),

  CONSTRAINT fk_task_constraints_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_constraints_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Task constraints';
