-- ====================================
-- calendars
-- Calendar container. Three kinds: personal (1:1 per user), shared
-- (TimeTree-style group calendar where person=color=layer), and system
-- (read-only holiday feeds injected at query time).
-- ====================================
CREATE TABLE calendars (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',

  kind ENUM('personal','shared','system') NOT NULL DEFAULT 'shared' COMMENT 'Calendar kind: personal (1:1 per user), shared (group), system (holidays)',
  name VARCHAR(255) NOT NULL COMMENT 'Display name',
  description TEXT NULL COMMENT 'Optional description',
  color VARCHAR(7) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL DEFAULT '#4285F4' COMMENT 'Default hex color',
  cover_url VARCHAR(1024) NULL COMMENT 'Cover image URL',

  owner_user_id INT UNSIGNED NULL COMMENT 'For personal calendars: the owning user. NULL for shared/system',
  system_slug VARCHAR(100) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'For system calendars: provider identifier (e.g., holidays.jp)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_calendars_public_id (public_id),
  UNIQUE KEY uniq_calendars_personal (workspace_id, owner_user_id, kind, enabled) COMMENT 'At most one enabled personal calendar per user per workspace',
  UNIQUE KEY uniq_calendars_system_slug (workspace_id, system_slug),
  KEY idx_calendars_workspace_id_kind (workspace_id, kind),

  CONSTRAINT fk_calendars_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_calendars_owner FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Calendar containers (personal/shared/system)';
