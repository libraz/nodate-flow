-- ====================================
-- ai_providers
-- LLM provider configurations. api_key_ciphertext stores AES-256-GCM
-- output (nonce + tag + ciphertext) and is NEVER read back via the API.
-- ====================================
CREATE TABLE ai_providers (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',

  kind ENUM('anthropic','openai','google','ollama','openai_compat') NOT NULL COMMENT 'Provider kind',
  name VARCHAR(255) NOT NULL COMMENT 'Human-readable label',
  base_url VARCHAR(2048) NULL COMMENT 'API base URL (required for openai_compat/ollama)',
  api_key_ciphertext VARBINARY(512) NOT NULL COMMENT 'AES-256-GCM nonce+tag+ciphertext',
  api_key_prefix CHAR(8) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Leading chars for display masking',
  api_key_suffix CHAR(4) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Trailing chars for display masking',
  default_model VARCHAR(128) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'Default model name',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_ai_providers_public_id (public_id),
  UNIQUE KEY uniq_ai_providers_workspace_public_id (workspace_id, public_id),
  KEY idx_ai_providers_workspace_id_enabled (workspace_id, enabled),

  CONSTRAINT fk_ai_providers_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='LLM provider credentials';
