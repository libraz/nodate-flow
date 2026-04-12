-- ====================================
-- webhook_deliveries
-- Tracks each delivery attempt for a webhook subscription. Append-mostly;
-- retries update the existing row with incremented attempts and new status.
-- ====================================
CREATE TABLE webhook_deliveries (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  subscription_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to webhook_subscriptions.id',

  event_type VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'The event that triggered this delivery',
  event_public_id BINARY(16) NULL COMMENT 'public_id of the source event',
  payload_json JSON NOT NULL COMMENT 'The JSON payload that was/will be sent',
  status ENUM('pending','delivering','delivered','failed','dead') NOT NULL DEFAULT 'pending' COMMENT 'Delivery state',
  http_status SMALLINT UNSIGNED NULL COMMENT 'HTTP response status from the target',
  response_body TEXT NULL COMMENT 'Truncated response body (first 4KB) for debugging',
  attempts TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Number of delivery attempts so far',
  max_attempts TINYINT UNSIGNED NOT NULL DEFAULT 6 COMMENT 'Maximum retry attempts',
  next_retry_at DATETIME NULL COMMENT 'When to retry next (null when delivered or dead)',
  delivered_at DATETIME NULL COMMENT 'When successfully delivered',
  failed_at DATETIME NULL COMMENT 'When marked dead (all retries exhausted)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_webhook_deliveries_public_id (public_id),
  KEY idx_webhook_deliveries_workspace_id_subscription_id_created_at (workspace_id, subscription_id, created_at DESC),
  KEY idx_webhook_deliveries_status_next_retry_at (status, next_retry_at),
  KEY idx_webhook_deliveries_workspace_id_status (workspace_id, status),

  CONSTRAINT fk_webhook_deliveries_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_webhook_deliveries_subscription FOREIGN KEY (subscription_id) REFERENCES webhook_subscriptions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Webhook delivery attempts and retry tracking';
