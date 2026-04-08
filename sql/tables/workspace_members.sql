-- ====================================
-- workspace_members
-- M:N between workspaces and users with a workspace-scoped role.
-- ====================================
CREATE TABLE workspace_members (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  user_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id',

  role ENUM('owner','admin','member','guest') NOT NULL DEFAULT 'member' COMMENT 'Workspace-level role',
  invited_by_user_id INT UNSIGNED NULL COMMENT 'Inviter user.id',
  invited_at DATETIME NULL COMMENT 'Invitation time',
  joined_at DATETIME NULL COMMENT 'Acceptance time',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_workspace_members_public_id (public_id),
  UNIQUE KEY uniq_workspace_members_workspace_id_user_id (workspace_id, user_id),
  KEY idx_workspace_members_user_id (user_id),

  CONSTRAINT fk_workspace_members_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_workspace_members_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_workspace_members_inviter FOREIGN KEY (invited_by_user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Workspace membership';
