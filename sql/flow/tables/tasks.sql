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
  updated_by_user_id INT UNSIGNED NULL COMMENT 'Last modifier user.id (audit field; NULL for system writers)',

  title VARCHAR(500) NOT NULL COMMENT 'Task title; length matches the API and MCP input limit',
  description MEDIUMTEXT NULL COMMENT 'Markdown body',
  derived_state ENUM('open','waiting','review','done','cancelled') NOT NULL DEFAULT 'open' COMMENT 'Computed from constraints + events; do NOT update directly',
  agent_memo JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT 'Per-task scratchpad for the assigned AI agent: last_thought, retry_count, last_error, handoff_status, handoff_reason, last_started_at, last_cost_cents. NOT NULL with default {} so sqlc-generated json.RawMessage scans cleanly; mapper treats {} as "no memo yet"',
  priority INT NOT NULL DEFAULT 0 COMMENT 'LLM-optimized heuristic priority',
  due_on DATE NULL COMMENT 'Deadline for task completion; drives constraint evaluation',
  started_on DATE NULL COMMENT 'Date work began on this task',
  completed_at DATETIME(3) NULL COMMENT 'Time derived_state transitioned to done',
  archived_at DATETIME(3) NULL COMMENT 'Set when task is archived (distinct from enabled)',

  visibility ENUM('public','project','private') NOT NULL DEFAULT 'public' COMMENT 'ACL Layer 4: public=workspace members, project=project members, private=task actors only',
  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_tasks_public_id (public_id),
  UNIQUE KEY uniq_tasks_workspace_public_id (workspace_id, public_id),
  KEY idx_tasks_workspace_id_project_id (workspace_id, project_id),
  KEY idx_tasks_workspace_id_due_on (workspace_id, due_on),
  KEY idx_tasks_workspace_id_derived_state (workspace_id, derived_state),
  UNIQUE KEY uniq_tasks_project_id_task_number (project_id, task_number),
  KEY idx_tasks_workspace_id_archived_at (workspace_id, archived_at),
  KEY idx_tasks_parent_task_id (parent_task_id),
  -- Supports keyset pagination on (created_at DESC, public_id DESC) for
  -- ListTasksForWorkspaceKeyset / ListTasksForProjectKeyset / ListMyTasksKeyset.
  KEY idx_tasks_workspace_id_keyset (workspace_id, created_at, public_id),
  -- Serves the item-consistency reconciler's enabled-mismatch scan, the
  -- one query in the product that reads across tenants: it looks for
  -- calendar_events still enabled under a soft-disabled task. The
  -- disabled side is the small side, so leading with enabled makes the
  -- scan a covering range over just those rows, and the trailing id
  -- carries the reconciler's resume cursor inside the same index.
  -- Deliberately not workspace-scoped: a cross-tenant sweep cannot use
  -- an index that leads with workspace_id, and every tenant-scoped
  -- reader already has a more selective index above.
  KEY idx_tasks_enabled_id (enabled, id),
  FULLTEXT KEY ft_tasks_title_description (title, description),

  CONSTRAINT fk_tasks_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_tasks_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  CONSTRAINT fk_tasks_parent FOREIGN KEY (parent_task_id) REFERENCES tasks(id) ON DELETE SET NULL,
  CONSTRAINT fk_tasks_creator FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_tasks_updated_by FOREIGN KEY (updated_by_user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='nodate-flow core task object';
