-- ====================================
-- timeboxes
-- Time-bounded work containers (sprints, iterations, cycles).
-- A timebox belongs to a workspace and optionally scopes to a project.
-- Tasks are associated via the timebox_tasks join table.
-- ====================================
CREATE TABLE timeboxes (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  project_id INT UNSIGNED NULL COMMENT 'Internal FK to projects.id (NULL = spans all projects)',
  creator_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id',

  name VARCHAR(255) NOT NULL COMMENT 'Display name (e.g., Sprint 12)',
  description TEXT NULL COMMENT 'Optional description or goals',
  starts_on DATE NOT NULL COMMENT 'Timebox start date',
  ends_on DATE NOT NULL COMMENT 'Timebox end date (must be > starts_on)',
  status ENUM('planned','active','completed','cancelled') NOT NULL DEFAULT 'planned' COMMENT 'Lifecycle status',
  archived_at DATETIME NULL COMMENT 'Set when timebox is archived (distinct from enabled)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_timeboxes_public_id (public_id),
  UNIQUE KEY uniq_timeboxes_workspace_id_name_enabled (workspace_id, name, enabled),
  KEY idx_timeboxes_workspace_id_archived_at (workspace_id, archived_at),
  KEY idx_timeboxes_workspace_id_status_enabled (workspace_id, status, enabled),
  KEY idx_timeboxes_workspace_id_project_id_enabled (workspace_id, project_id, enabled),

  CONSTRAINT fk_timeboxes_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_timeboxes_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
  CONSTRAINT fk_timeboxes_creator FOREIGN KEY (creator_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Time-bounded work containers';
