-- ====================================
-- project_members
-- Project-level access override. A user must already be a workspace_member.
-- ====================================
CREATE TABLE project_members (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  project_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to projects.id',
  user_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id',

  role ENUM('lead','editor','commenter','viewer') NOT NULL DEFAULT 'editor' COMMENT 'Project-level role',
  added_at DATETIME(3) NULL COMMENT 'Time added to project',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_project_members_public_id (public_id),
  UNIQUE KEY uniq_project_members_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_project_members_project_id_user_id (project_id, user_id),
  KEY idx_project_members_workspace_id_user_id (workspace_id, user_id),

  CONSTRAINT fk_project_members_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_project_members_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  CONSTRAINT fk_project_members_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Project membership';
