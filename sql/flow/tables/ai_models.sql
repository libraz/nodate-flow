-- ====================================
-- ai_models
-- Concrete model offered by an ai_providers row. Pricing is stored as
-- micro-USD per 1M tokens to keep integer math.
-- ====================================
CREATE TABLE ai_models (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  provider_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to ai_providers.id',

  name VARCHAR(128) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Provider model identifier (e.g., claude-opus-4)',
  display_name VARCHAR(255) NOT NULL COMMENT 'Human-readable label',
  context_window INT UNSIGNED NOT NULL COMMENT 'Max context tokens',
  max_output_tokens INT UNSIGNED NULL COMMENT 'Max generated tokens (null = same as context)',
  input_price_micro_usd_per_mtok BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Input price in micro-USD per 1M tokens',
  output_price_micro_usd_per_mtok BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Output price in micro-USD per 1M tokens',
  supports_tools BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Whether model supports tool use',
  supports_vision BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Whether model supports image input',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_ai_models_public_id (public_id),
  UNIQUE KEY uniq_ai_models_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_ai_models_provider_id_name (provider_id, name),
  KEY idx_ai_models_workspace_id_enabled (workspace_id, enabled),

  CONSTRAINT fk_ai_models_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_ai_models_provider FOREIGN KEY (provider_id) REFERENCES ai_providers(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Concrete LLM models per provider';
