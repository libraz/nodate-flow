-- ====================================
-- users
-- Account-level principals. Authenticated via identities (local/oidc).
-- Not workspace-scoped; users are global and join workspaces via workspace_members.
-- ====================================
CREATE TABLE users (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',

  email VARCHAR(255) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Primary email, ASCII only',
  email_verified_at DATETIME NULL COMMENT 'Email verification timestamp',
  display_name VARCHAR(255) NOT NULL COMMENT 'Human-readable name',
  avatar_url VARCHAR(1024) NULL COMMENT 'Avatar image URL',
  locale VARCHAR(16) NOT NULL DEFAULT 'en' COMMENT 'Preferred locale tag (BCP 47)',
  theme_preference ENUM('aurora-light','aurora-dark','dotline-light','dotline-dark','system') NOT NULL DEFAULT 'system' COMMENT 'UI theme preference',
  last_login_at DATETIME NULL COMMENT 'Last successful login',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_users_public_id (public_id),
  UNIQUE KEY uniq_users_email (email),
  KEY idx_users_enabled (enabled)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Global user accounts';
