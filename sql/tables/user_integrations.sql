-- ====================================
-- user_integrations
-- Personal OAuth connections (GitHub / Slack / Google Calendar) owned
-- by an individual user. Separate from workspace-level integrations
-- managed by @mcp. Tokens are encrypted with NF_SECRET_KEY (same
-- cipher as the AI provider credentials).
-- ====================================
CREATE TABLE user_integrations (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  user_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id',

  provider ENUM('github','slack','google_calendar') NOT NULL COMMENT 'OAuth provider kind',
  external_account_id VARCHAR(255) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Provider subject (GH login, Slack user id, Google sub)',
  external_account_label VARCHAR(255) NOT NULL COMMENT 'Display-only label (email or @handle)',
  scopes TEXT NOT NULL COMMENT 'Space-separated list of granted OAuth scopes',

  access_token_ciphertext VARBINARY(4096) NOT NULL COMMENT 'AES-256-GCM encrypted access token',
  refresh_token_ciphertext VARBINARY(4096) NULL COMMENT 'AES-256-GCM encrypted refresh token (nullable: GitHub OAuth apps do not issue one)',
  access_token_expires_at DATETIME(3) NULL COMMENT 'Access token expiry (providers that issue long-lived tokens leave this NULL)',

  connected_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) COMMENT 'When the user first authorised the app',
  last_refreshed_at DATETIME(3) NULL COMMENT 'Last successful token refresh',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_user_integrations_public_id (public_id),
  UNIQUE KEY uniq_user_integrations_user_provider (user_id, provider),
  KEY idx_user_integrations_user_id (user_id),

  CONSTRAINT fk_user_integrations_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Personal OAuth integrations owned by an individual user';
