-- ====================================
-- ai_suggestion_batches
-- Immutable provenance for actor-private AI suggestion output.
-- ====================================
CREATE TABLE ai_suggestion_batches (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible batch id',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  user_id INT UNSIGNED NULL COMMENT 'Initiating user; NULL means the owner was deleted and access is denied',
  input_count INT UNSIGNED NOT NULL COMMENT 'Expected number of normalized task provenance references',
  provenance_valid BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'True only after the complete input set is stored atomically',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_ai_suggestion_batches_public_id (public_id),
  UNIQUE KEY uniq_ai_suggestion_batches_workspace_public_id (workspace_id, public_id),
  KEY idx_ai_suggestion_batches_workspace_user_created (workspace_id, user_id, created_at),

  CONSTRAINT fk_ai_suggestion_batches_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_ai_suggestion_batches_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Actor-private suggestion input provenance';
