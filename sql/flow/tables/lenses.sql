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
  -- Scope value for the name-uniqueness key below. project_id stays
  -- nullable because NULL is what "workspace-wide" means and the foreign
  -- key cascade depends on it, but a unique index never treats an entry
  -- containing NULL as a duplicate: keyed on project_id directly, the
  -- key bound only project-scoped lenses and let a workspace hold any
  -- number of live workspace-wide lenses called the same thing. See
  -- sql/core/conformance/schema/50-nullable-unique-keys.sql.
  scope_project_id INT UNSIGNED GENERATED ALWAYS AS (IFNULL(project_id, 0)) VIRTUAL NOT NULL COMMENT 'The project this lens is scoped to, or 0 when it is workspace-wide. Exists only so the name-uniqueness key binds on workspace-wide lenses; AUTO_INCREMENT never issues 0, so it cannot name a real project.',
  creator_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id',

  name VARCHAR(100) NOT NULL COMMENT 'Display name',
  description VARCHAR(500) NULL COMMENT 'Optional public-facing description shown on the share page',
  lens_json JSON NOT NULL COMMENT 'Serialized Lens object (filter, sort, groupBy)',
  is_default BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Default lens for the scope',
  is_public BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Whether the lens is publicly shared',
  public_token_hash CHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'SHA-256 hex of the public share URL token; the plaintext is handed to the publisher once and never stored, so a leak of this table does not yield working share URLs. NULL while the lens is not published. Same shape as calendar_public_shares.token_hash — capability tokens are hashed at rest everywhere.',
  shared_at DATETIME(3) NULL COMMENT 'Timestamp when first shared publicly',
  safety_checked_at DATETIME(3) NULL COMMENT 'Timestamp of last AI safety check',
  archived_at DATETIME(3) NULL COMMENT 'Set when lens is archived (distinct from enabled)',

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

  UNIQUE KEY uniq_lenses_public_id (public_id),
  UNIQUE KEY uniq_lenses_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_lenses_workspace_id_scope_project_id_name_active (workspace_id, scope_project_id, name, active),
  UNIQUE KEY uniq_lenses_public_token_hash (public_token_hash),
  KEY idx_lenses_workspace_id_archived_at (workspace_id, archived_at),
  KEY idx_lenses_workspace_id_project_id_enabled (workspace_id, project_id, enabled),
  KEY idx_lenses_workspace_id_creator_id (workspace_id, creator_id),

  CONSTRAINT fk_lenses_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_lenses_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
  CONSTRAINT fk_lenses_creator FOREIGN KEY (creator_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Saved task query views';
