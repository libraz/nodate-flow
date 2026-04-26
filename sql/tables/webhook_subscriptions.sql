-- ====================================
-- webhook_subscriptions
-- Workspace-level webhook subscription. When events occur, matching
-- subscriptions trigger deliveries to the configured URL endpoint.
-- ====================================
CREATE TABLE webhook_subscriptions (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  creator_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id, who created this subscription',

  url VARCHAR(2048) NOT NULL COMMENT 'Delivery endpoint URL',
  secret VARCHAR(255) NOT NULL COMMENT 'HMAC-SHA256 shared secret for signing deliveries',
  description VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'Human-readable description',
  event_types JSON NOT NULL COMMENT 'JSON array of event type patterns to match (e.g. ["task.created","task.updated"])',
  is_active BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Whether deliveries are sent',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_webhook_subscriptions_public_id (public_id),
  UNIQUE KEY uniq_webhook_subscriptions_workspace_public_id (workspace_id, public_id),
  KEY idx_webhook_subscriptions_workspace_id_is_active (workspace_id, is_active),
  KEY idx_webhook_subscriptions_workspace_id_creator_id (workspace_id, creator_id),

  CONSTRAINT fk_webhook_subscriptions_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_webhook_subscriptions_creator FOREIGN KEY (creator_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Workspace-level webhook subscriptions';
