-- ====================================
-- ai_agents
-- Reusable LLM agent configuration: a model + system prompt + defaults.
-- Agents are referenced by automations and MCP tools.
-- ====================================
CREATE TABLE ai_agents (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  model_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to ai_models.id',

  name VARCHAR(255) NOT NULL COMMENT 'Human-readable agent name',
  description TEXT NULL COMMENT 'Free-form description',
  system_prompt MEDIUMTEXT NOT NULL COMMENT 'System prompt text',
  temperature SMALLINT UNSIGNED NOT NULL DEFAULT 100 COMMENT 'Sampling temperature x100 (e.g., 100 = 1.00)',
  max_output_tokens INT UNSIGNED NULL COMMENT 'Per-call output cap (null = model default)',
  tools_json JSON NULL COMMENT 'Allowed tool list as JSON array',
  allowed_scopes_json JSON NULL COMMENT 'Allowed MCP scope list as JSON array (null = inherit from token)',
  monthly_cost_cap_cents INT UNSIGNED NULL COMMENT 'Monthly spend cap in USD cents (null = no cap)',
  schedule_kind ENUM('disabled','interval','on_event','manual') NOT NULL DEFAULT 'disabled' COMMENT 'Trigger mode: interval = fires every NF_AGENT_TICK_INTERVAL; on_event = fires from eventbus; manual = only via /agents/{id}/trigger',
  event_trigger_types JSON NULL COMMENT 'JSON array of eventbus Kind strings that fire this agent when schedule_kind=on_event (e.g., ["signal.attached","task.transition.submit"])',
  paused BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Manually or automatically paused (e.g., cost cap exceeded)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_ai_agents_public_id (public_id),
  UNIQUE KEY uniq_ai_agents_workspace_public_id (workspace_id, public_id),
  KEY idx_ai_agents_workspace_id_enabled (workspace_id, enabled),
  KEY idx_ai_agents_model_id (model_id),

  CONSTRAINT fk_ai_agents_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_ai_agents_model FOREIGN KEY (model_id) REFERENCES ai_models(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Reusable LLM agent configurations';
