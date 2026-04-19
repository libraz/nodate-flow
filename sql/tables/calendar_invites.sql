-- ====================================
-- calendar_invites
-- Shareable invite links for joining a calendar. Supports optional
-- expiration, max usage count, and role assignment on acceptance.
-- ====================================
CREATE TABLE calendar_invites (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  calendar_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to calendars.id',
  created_by_user_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id (invite creator)',

  token_hash CHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'SHA-256 hex of invite token plaintext',
  role ENUM('manager','editor','viewer') NOT NULL DEFAULT 'editor' COMMENT 'Role granted on acceptance',
  max_uses INT UNSIGNED NULL COMMENT 'Max number of uses; NULL = unlimited',
  use_count INT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Current number of uses',
  expires_at DATETIME NULL COMMENT 'Expiration time; NULL = never expires',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_calendar_invites_public_id (public_id),
  UNIQUE KEY uniq_calendar_invites_token_hash (token_hash),
  KEY idx_calendar_invites_calendar (workspace_id, calendar_id),

  CONSTRAINT fk_calendar_invites_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_calendar_invites_calendar FOREIGN KEY (calendar_id) REFERENCES calendars(id) ON DELETE CASCADE,
  CONSTRAINT fk_calendar_invites_creator FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Calendar invite links';
