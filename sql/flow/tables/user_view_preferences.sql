-- ====================================
-- user_view_preferences
-- Per-user per-scope display preferences (view mode, grouping,
-- density, column visibility, etc.) stored as JSON.
-- ====================================
CREATE TABLE user_view_preferences (
  id              INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id       BINARY(16) NOT NULL                    COMMENT 'UUID v7, the only externally visible ID',
  workspace_id    INT UNSIGNED NOT NULL,
  user_id         INT UNSIGNED NOT NULL,

  scope_type      ENUM('workspace','project','lens','timebox') NOT NULL,
  scope_public_id BINARY(16) NULL COMMENT 'public_id of the scoped entity; NULL for workspace scope, which the row''s own workspace already identifies',
  -- Scope value for the uniqueness key below. A unique index never treats
  -- an entry containing NULL as a duplicate, so keyed on scope_public_id
  -- directly the key bound nothing at workspace scope — the scope most
  -- rows carry. The upsert then found no row to update and appended a new
  -- one on every save, leaving the read to return whichever of the
  -- accumulated rows the index reached first.
  --
  -- The substitute for NULL is an all-zero id, which no scope can carry:
  -- every public_id is a UUID v7 and therefore has the version nibble set.
  scope_key       BINARY(16) GENERATED ALWAYS AS (IFNULL(scope_public_id, 0x00000000000000000000000000000000)) VIRTUAL NOT NULL COMMENT 'The scope this row applies to: scope_public_id, or all zero bytes at workspace scope. Exists only so the uniqueness key below binds on workspace-scoped rows.',
  prefs_json      JSON NOT NULL COMMENT 'view_mode, group_by, density, column_order, hidden_columns...',

  sort_weight     INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes           TEXT NULL COMMENT 'Admin notes',
  enabled         BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at      TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_user_view_prefs_public_id (public_id),
  UNIQUE KEY uniq_user_view_prefs_workspace_public_id (workspace_id, public_id),
  -- One preference row per (workspace, user, scope). workspace_id leads
  -- the key because the workspace is part of the scope, not a filter on
  -- it: without it a user who belongs to two workspaces has one
  -- workspace-scoped row for both, and saving a preference in one
  -- overwrites the other. The key doubles as the (workspace_id, user_id)
  -- lookup index, so no separate one is declared.
  UNIQUE KEY uniq_user_view_prefs_user_scope (workspace_id, user_id, scope_type, scope_key),
  -- Bare user_id index for ON DELETE CASCADE on users. The unique key
  -- above leads with workspace_id and cannot serve a user_id-only lookup,
  -- and leaving it out would have InnoDB create an unnamed one instead.
  KEY idx_user_view_prefs_user_id (user_id),

  CONSTRAINT fk_user_view_prefs_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_user_view_prefs_user      FOREIGN KEY (user_id)      REFERENCES users(id)      ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Per-user per-scope display preferences';
