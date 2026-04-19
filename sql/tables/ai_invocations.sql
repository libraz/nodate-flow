-- ====================================
-- ai_invocations
-- LLM call audit. Prompts and responses are stored redacted; raw secrets
-- must never appear here.
-- ====================================
CREATE TABLE ai_invocations (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  provider_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to ai_providers.id',
  user_id INT UNSIGNED NULL COMMENT 'Internal FK to users.id (if user-initiated)',
  agent_id INT UNSIGNED NULL COMMENT 'Internal FK to ai_agents.id when the call was made on behalf of an AI agent',
  task_id INT UNSIGNED NULL COMMENT 'Internal FK to tasks.id if applicable',

  purpose VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Logical call purpose (e.g., propose_tasks)',
  model VARCHAR(128) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Model name actually used',
  prompt_redacted MEDIUMTEXT NOT NULL COMMENT 'Redacted prompt text',
  response_redacted MEDIUMTEXT NULL COMMENT 'Redacted response text',
  tokens_input INT UNSIGNED NULL COMMENT 'Input token count',
  tokens_output INT UNSIGNED NULL COMMENT 'Output token count',
  cost_estimate DECIMAL(10,6) NULL COMMENT 'Estimated cost (USD)',
  status ENUM('ok','error','blocked') NOT NULL COMMENT 'Outcome',
  error_code VARCHAR(128) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'Error code when status != ok',
  invoked_at DATETIME NOT NULL COMMENT 'Invocation time (second precision)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_ai_invocations_public_id (public_id),
  KEY idx_ai_invocations_workspace_id_invoked_at (workspace_id, invoked_at),
  KEY idx_ai_invocations_workspace_id_provider_id (workspace_id, provider_id),
  KEY idx_ai_invocations_agent_id_invoked_at (agent_id, invoked_at),

  CONSTRAINT fk_ai_invocations_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_ai_invocations_provider FOREIGN KEY (provider_id) REFERENCES ai_providers(id) ON DELETE CASCADE,
  CONSTRAINT fk_ai_invocations_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_ai_invocations_agent FOREIGN KEY (agent_id) REFERENCES ai_agents(id) ON DELETE SET NULL,
  CONSTRAINT fk_ai_invocations_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='LLM invocation audit';
