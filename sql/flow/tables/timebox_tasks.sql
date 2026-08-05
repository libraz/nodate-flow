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
  -- Liveness marker scoping the unique key below to live rows: 1 while
  -- enabled, NULL once soft-deleted, so tombstones leave the index
  -- rather than colliding with each other. See the soft-delete rule in
  -- sql/core/conformance/schema/40-soft-delete-uniqueness.sql.
  active TINYINT UNSIGNED GENERATED ALWAYS AS (IF(enabled, 1, NULL)) VIRTUAL COMMENT 'NULL once soft-deleted; exists only to scope the unique key below to live rows',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_timebox_tasks_public_id (public_id),
  UNIQUE KEY uniq_timebox_tasks_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_timebox_tasks_timebox_id_task_id_active (timebox_id, task_id, active),
  KEY idx_timebox_tasks_workspace_id_timebox_id (workspace_id, timebox_id),
  KEY idx_timebox_tasks_task_id (task_id),

  CONSTRAINT fk_timebox_tasks_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_timebox_tasks_timebox FOREIGN KEY (timebox_id) REFERENCES timeboxes(id) ON DELETE CASCADE,
  CONSTRAINT fk_timebox_tasks_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Timebox-task associations';
