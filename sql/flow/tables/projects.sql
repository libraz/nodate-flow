-- ====================================
-- projects
-- Workspace-scoped container for tasks.
-- ====================================
CREATE TABLE projects (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',

  slug VARCHAR(63) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Workspace-local slug',
  identifier CHAR(5) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'Human-readable project key (e.g. NF); NULL when not assigned',
  name VARCHAR(255) NOT NULL COMMENT 'Display name',
  description TEXT NULL COMMENT 'Optional description',
  color VARCHAR(16) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'Hex color (e.g., #1abc9c)',
  is_archived BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Archived flag (distinct from enabled)',
  started_on DATE NULL COMMENT 'Project start date',
  ended_on DATE NULL COMMENT 'Project end date',
  feature_pages BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Toggle pages feature',
  feature_timeboxes BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Toggle timeboxes feature',
  feature_lenses BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Toggle lenses feature',
  feature_calendar BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Toggle calendar feature',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  -- Liveness marker scoping the unique key below to live rows: 1 while
  -- enabled, NULL once soft-deleted, so tombstones leave the index
  -- rather than colliding with each other. See the soft-delete rule in
  -- sql/core/conformance/schema/40-soft-delete-uniqueness.sql.
  active TINYINT UNSIGNED GENERATED ALWAYS AS (IF(enabled, 1, NULL)) VIRTUAL COMMENT 'NULL once soft-deleted; exists only to scope the unique key below to live rows',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_projects_public_id (public_id),
  UNIQUE KEY uniq_projects_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_projects_workspace_id_slug_active (workspace_id, slug, active),
  KEY idx_projects_workspace_id_enabled (workspace_id, enabled),
  UNIQUE KEY uniq_projects_workspace_id_identifier_active (workspace_id, identifier, active),

  CONSTRAINT fk_projects_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Task container';
