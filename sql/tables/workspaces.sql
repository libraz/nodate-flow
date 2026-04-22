-- ====================================
-- workspaces
-- Top-level tenant boundary. Every workspace-scoped table carries
-- workspace_id as its leading composite index column.
-- ====================================
CREATE TABLE workspaces (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',

  slug VARCHAR(63) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'DNS-label slug (RFC 1035)',
  name VARCHAR(255) NOT NULL COMMENT 'Display name',
  description TEXT NULL COMMENT 'Optional description',
  icon_url VARCHAR(2048) NULL COMMENT 'Icon image URL',

  timezone VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL DEFAULT 'UTC' COMMENT 'Default IANA timezone for the workspace; user tz overrides per-user',
  country CHAR(2) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'ISO 3166-1 alpha-2 country; drives default holiday subscription',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_workspaces_public_id (public_id),
  UNIQUE KEY uniq_workspaces_slug (slug),
  KEY idx_workspaces_enabled (enabled)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Tenant boundary';
