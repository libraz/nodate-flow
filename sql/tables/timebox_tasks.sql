-- ====================================
-- timebox_tasks
-- Join table associating tasks with timeboxes.
-- A task can belong to multiple timeboxes (e.g., carried over).
-- ====================================
CREATE TABLE timebox_tasks (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  timebox_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to timeboxes.id',
  task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_timebox_tasks_public_id (public_id),
  UNIQUE KEY uniq_timebox_tasks_timebox_id_task_id_enabled (timebox_id, task_id, enabled),
  KEY idx_timebox_tasks_workspace_id_timebox_id (workspace_id, timebox_id),
  KEY idx_timebox_tasks_task_id (task_id),

  CONSTRAINT fk_timebox_tasks_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_timebox_tasks_timebox FOREIGN KEY (timebox_id) REFERENCES timeboxes(id) ON DELETE CASCADE,
  CONSTRAINT fk_timebox_tasks_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Timebox-task associations';
