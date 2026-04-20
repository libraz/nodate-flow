-- ====================================
-- labels
-- Hierarchical colored labels. Can be workspace-wide (project_id IS NULL)
-- or project-scoped.
-- ====================================
CREATE TABLE labels (
  id              INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id       BINARY(16) NOT NULL                    COMMENT 'UUID v7, the only externally visible ID',
  workspace_id    INT UNSIGNED NOT NULL                   COMMENT 'Internal FK to workspaces.id',
  project_id      INT UNSIGNED NULL                       COMMENT 'NULL = workspace-wide label',
  parent_label_id INT UNSIGNED NULL                       COMMENT 'Self-ref for hierarchy; NULL = root',

  name            VARCHAR(64) NOT NULL COMMENT 'Display name',
  color           VARCHAR(16) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL DEFAULT '#6b7280' COMMENT 'Hex color',
  description     VARCHAR(255) NULL COMMENT 'Optional description',

  sort_weight     INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes           TEXT NULL COMMENT 'Admin notes',
  enabled         BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_labels_public_id (public_id),
  UNIQUE KEY uniq_labels_workspace_id_project_id_name_enabled (workspace_id, project_id, name, enabled),
  KEY idx_labels_workspace_id_project_id_enabled (workspace_id, project_id, enabled),
  KEY idx_labels_parent_label_id (parent_label_id),

  CONSTRAINT fk_labels_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_labels_project   FOREIGN KEY (project_id)   REFERENCES projects(id)   ON DELETE CASCADE,
  CONSTRAINT fk_labels_parent    FOREIGN KEY (parent_label_id) REFERENCES labels(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Hierarchical colored labels';
