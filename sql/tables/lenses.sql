-- ====================================
-- lenses
-- Saved views (filter + sort + groupBy) for task queries.
-- Can be workspace-wide (project_id IS NULL) or project-scoped.
-- ====================================
CREATE TABLE lenses (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  project_id INT UNSIGNED NULL COMMENT 'Internal FK to projects.id (NULL = workspace-wide)',
  creator_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id',

  name VARCHAR(100) NOT NULL COMMENT 'Display name',
  lens_json JSON NOT NULL COMMENT 'Serialized Lens object (filter, sort, groupBy)',
  is_default BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Default lens for the scope',
  is_public BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Whether the lens is publicly shared',
  public_token CHAR(32) CHARACTER SET latin1 NULL COMMENT 'Random hex token for public share URL',
  shared_at DATETIME NULL COMMENT 'Timestamp when first shared publicly',
  safety_checked_at DATETIME NULL COMMENT 'Timestamp of last AI safety check',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_lenses_public_id (public_id),
  UNIQUE KEY uniq_lenses_workspace_id_project_id_name_enabled (workspace_id, project_id, name, enabled),
  UNIQUE KEY uniq_lenses_public_token (public_token),
  KEY idx_lenses_workspace_id_project_id_enabled (workspace_id, project_id, enabled),
  KEY idx_lenses_workspace_id_creator_id (workspace_id, creator_id),

  CONSTRAINT fk_lenses_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_lenses_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  CONSTRAINT fk_lenses_creator FOREIGN KEY (creator_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Saved task query views';
