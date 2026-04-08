-- ====================================
-- ai_settings
-- Per-workspace AI configuration introduced in Phase 2 Wave 2 (ADR 0003).
-- Holds duplicate-detection thresholds and the embed-budget bucket that
-- the CostGuard tracks separately from the LLM chat budget.
--
-- Settings are not user-facing entities, so no public_id: the row is
-- addressed by its parent workspace.
-- ====================================
CREATE TABLE ai_settings (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',

  embed_model              VARCHAR(64)  NOT NULL DEFAULT 'mock-768' COMMENT 'Embedding model key (resolved by ai/embed registry)',
  embed_budget_cents_day   INT UNSIGNED NOT NULL DEFAULT 100 COMMENT 'Daily embed cost cap in cents (separate bucket from chat budget)',
  duplicate_threshold_high DECIMAL(4,3) NOT NULL DEFAULT 0.870 COMMENT 'Cosine sim >= this -> duplicate candidate',
  duplicate_threshold_low  DECIMAL(4,3) NOT NULL DEFAULT 0.750 COMMENT 'Cosine sim in [low, high) -> related task',

  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_ai_settings_workspace (workspace_id),

  CONSTRAINT fk_ai_settings_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Per-workspace AI configuration (ADR 0003)';
