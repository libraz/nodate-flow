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
  -- Scope value for the name-uniqueness key below. project_id stays
  -- nullable because NULL is what "workspace-wide" means and the foreign
  -- key cascade depends on it, but a unique index never treats an entry
  -- containing NULL as a duplicate: keyed on project_id directly, the
  -- key bound only project-scoped labels and let a workspace hold any
  -- number of live workspace-wide labels called the same thing. See
  -- sql/core/conformance/schema/50-nullable-unique-keys.sql.
  scope_project_id INT UNSIGNED GENERATED ALWAYS AS (IFNULL(project_id, 0)) VIRTUAL NOT NULL COMMENT 'The project this label is scoped to, or 0 when it is workspace-wide. Exists only so the name-uniqueness key binds on workspace-wide labels; AUTO_INCREMENT never issues 0, so it cannot name a real project.',
  parent_label_id INT UNSIGNED NULL                       COMMENT 'Self-ref for hierarchy; NULL = root',
  created_by_user_id INT UNSIGNED NULL                    COMMENT 'Creator user.id (audit field; NULL when creator is removed)',

  name            VARCHAR(64) NOT NULL COMMENT 'Display name',
  color           VARCHAR(16) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL DEFAULT '#6b7280' COMMENT 'Hex color',
  description     VARCHAR(255) NULL COMMENT 'Optional description',

  sort_weight     INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes           TEXT NULL COMMENT 'Admin notes',
  enabled         BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  -- Liveness marker scoping the unique key below to live rows: 1 while
  -- enabled, NULL once soft-deleted, so tombstones leave the index
  -- rather than colliding with each other. See the soft-delete rule in
  -- sql/core/conformance/schema/40-soft-delete-uniqueness.sql.
  active TINYINT UNSIGNED GENERATED ALWAYS AS (IF(enabled, 1, NULL)) VIRTUAL COMMENT 'NULL once soft-deleted; exists only to scope the unique key below to live rows',
  updated_at      TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_labels_public_id (public_id),
  UNIQUE KEY uniq_labels_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_labels_workspace_id_scope_project_id_name_active (workspace_id, scope_project_id, name, active),
  KEY idx_labels_workspace_id_project_id_enabled (workspace_id, project_id, enabled),
  KEY idx_labels_parent_label_id (parent_label_id),
  KEY idx_labels_created_by_user_id (created_by_user_id),

  CONSTRAINT fk_labels_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_labels_project   FOREIGN KEY (project_id)   REFERENCES projects(id)   ON DELETE CASCADE,
  CONSTRAINT fk_labels_parent    FOREIGN KEY (parent_label_id) REFERENCES labels(id) ON DELETE SET NULL,
  CONSTRAINT fk_labels_creator   FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Hierarchical colored labels';
