-- ====================================
-- task_event_links
-- M:N between tasks and calendar_events. Parallel to task_dependencies
-- (task-to-task). Encodes contribution/dependency semantics BETWEEN a
-- task and an umbrella event or milestone; distinct from the 1:1
-- projection relation stored via calendar_events.task_id + task_role.
-- ====================================
CREATE TABLE task_event_links (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id',
  event_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to calendar_events.id',

  relation ENUM('contributes_to','blocks','depends_on','prep_for') NOT NULL DEFAULT 'contributes_to'
    COMMENT 'contributes_to = task is work toward an umbrella event; blocks/depends_on/prep_for reserved for future',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order within an event''s linked-task list',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag (soft-disable)',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_task_event_links_public_id (public_id),
  UNIQUE KEY uniq_task_event_links_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_task_event_links_task_event_relation (task_id, event_id, relation, enabled) COMMENT 'At most one enabled link per (task, event, relation)',
  KEY idx_task_event_links_workspace_event (workspace_id, event_id, enabled),
  KEY idx_task_event_links_workspace_task (workspace_id, task_id, enabled),

  CONSTRAINT fk_task_event_links_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_event_links_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_event_links_event FOREIGN KEY (event_id) REFERENCES calendar_events(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='M:N task-to-event relationships (umbrella events, milestones)';
