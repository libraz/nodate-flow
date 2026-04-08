-- ====================================
-- mcp_tokens
-- Personal access tokens used by MCP clients (Claude Desktop, mcp-cli, ...).
-- Only the SHA-256 hash is stored.
-- ====================================
CREATE TABLE mcp_tokens (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  user_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id (token owner)',
  agent_id INT UNSIGNED NULL COMMENT 'Internal FK to ai_agents.id when the token acts on behalf of an AI agent (2.MCP-2)',

  name VARCHAR(255) NOT NULL COMMENT 'Human-readable label',
  token_hash CHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'SHA-256 hex of the bearer token',
  token_prefix CHAR(8) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Leading chars shown as hint',
  scopes_json JSON NOT NULL COMMENT 'Array of granted MCP tool scopes',
  expires_at DATETIME NULL COMMENT 'Expiry time (null = never)',
  last_used_at DATETIME NULL COMMENT 'Last successful use',
  revoked_at DATETIME NULL COMMENT 'Explicit revocation time',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_mcp_tokens_public_id (public_id),
  UNIQUE KEY uniq_mcp_tokens_token_hash (token_hash),
  KEY idx_mcp_tokens_workspace_id_user_id (workspace_id, user_id),

  CONSTRAINT fk_mcp_tokens_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_mcp_tokens_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_mcp_tokens_agent FOREIGN KEY (agent_id) REFERENCES ai_agents(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='MCP personal access tokens';
