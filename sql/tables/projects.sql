-- ====================================
-- projects
-- Workspace-scoped container for tasks.
-- ====================================
CREATE TABLE projects (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',

  slug VARCHAR(63) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Workspace-local slug',
  name VARCHAR(255) NOT NULL COMMENT 'Display name',
  description TEXT NULL COMMENT 'Optional description',
  color VARCHAR(16) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'Hex color (e.g., #1abc9c)',
  is_archived BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Archived flag (distinct from enabled)',
  started_on DATE NULL COMMENT 'Project start date',
  ended_on DATE NULL COMMENT 'Project end date',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_projects_public_id (public_id),
  UNIQUE KEY uniq_projects_workspace_id_slug_enabled (workspace_id, slug, enabled),
  KEY idx_projects_workspace_id_enabled (workspace_id, enabled),

  CONSTRAINT fk_projects_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Task container';
