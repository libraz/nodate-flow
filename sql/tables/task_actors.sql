-- ====================================
-- task_actors
-- Users attached to a task in a given role (assignee, reviewer, etc.).
-- ====================================
CREATE TABLE task_actors (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id',
  user_id INT UNSIGNED NULL COMMENT 'Internal FK to users.id (null when this row is an AI agent actor)',
  agent_id INT UNSIGNED NULL COMMENT 'Internal FK to ai_agents.id (null when this row is a human actor)',
  kind ENUM('user','agent') NOT NULL DEFAULT 'user' COMMENT 'Actor kind — user or AI agent',

  role ENUM('assignee','reviewer','watcher','approver') NOT NULL DEFAULT 'assignee' COMMENT 'Actor role on the task',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_task_actors_public_id (public_id),
  UNIQUE KEY uniq_task_actors_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_task_actors_task_id_user_id_role (task_id, user_id, role),
  UNIQUE KEY uniq_task_actors_task_id_agent_id_role (task_id, agent_id, role),
  KEY idx_task_actors_workspace_id_user_id (workspace_id, user_id),
  KEY idx_task_actors_workspace_id_agent_id (workspace_id, agent_id),

  CONSTRAINT fk_task_actors_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_actors_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_actors_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_actors_agent FOREIGN KEY (agent_id) REFERENCES ai_agents(id) ON DELETE CASCADE,
  CONSTRAINT chk_task_actors_kind_target CHECK (
    (kind = 'user'  AND user_id  IS NOT NULL AND agent_id IS NULL) OR
    (kind = 'agent' AND agent_id IS NOT NULL AND user_id  IS NULL)
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Task actors (assignees/reviewers/...)';
