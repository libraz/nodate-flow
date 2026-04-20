-- ====================================
-- tasks
-- nodate-flow's central object. derived_state is computed from
-- constraints + events and MUST NOT be updated directly by the API;
-- the event bus is the sole writer.
-- ====================================
CREATE TABLE tasks (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  project_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to projects.id',
  task_number INT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Per-project monotonic counter (1-based)',
  parent_task_id INT UNSIGNED NULL COMMENT 'Self-reference for subtasks',
  created_by_user_id INT UNSIGNED NULL COMMENT 'Creator user.id',

  title VARCHAR(255) NOT NULL COMMENT 'Task title',
  description MEDIUMTEXT NULL COMMENT 'Markdown body',
  derived_state ENUM('open','waiting','review','done','cancelled') NOT NULL DEFAULT 'open' COMMENT 'Computed from constraints + events; do NOT update directly',
  priority INT NOT NULL DEFAULT 0 COMMENT 'LLM-optimized heuristic priority',
  due_on DATE NULL COMMENT 'Deadline for task completion; drives constraint evaluation',
  started_on DATE NULL COMMENT 'Date work began on this task',
  event_on DATE NULL COMMENT 'External reference date (meeting, launch, milestone) this task relates to; NOT a constraint — use deadline constraint for enforcement',
  completed_at DATETIME NULL COMMENT 'Time derived_state transitioned to done',
  archived_at DATETIME NULL COMMENT 'Set when task is archived (distinct from enabled)',

  visibility ENUM('public','project','private') NOT NULL DEFAULT 'public' COMMENT 'ACL Layer 4: public=workspace members, project=project members, private=task actors only',
  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_tasks_public_id (public_id),
  KEY idx_tasks_workspace_id_project_id (workspace_id, project_id),
  KEY idx_tasks_workspace_id_due_on (workspace_id, due_on),
  KEY idx_tasks_workspace_id_event_on (workspace_id, event_on),
  KEY idx_tasks_workspace_id_derived_state (workspace_id, derived_state),
  UNIQUE KEY uniq_tasks_project_id_task_number (project_id, task_number),
  KEY idx_tasks_workspace_id_archived_at (workspace_id, archived_at),
  KEY idx_tasks_parent_task_id (parent_task_id),
  FULLTEXT KEY ft_tasks_title_description (title, description),

  CONSTRAINT fk_tasks_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_tasks_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  CONSTRAINT fk_tasks_parent FOREIGN KEY (parent_task_id) REFERENCES tasks(id) ON DELETE SET NULL,
  CONSTRAINT fk_tasks_creator FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='nodate-flow core task object';
