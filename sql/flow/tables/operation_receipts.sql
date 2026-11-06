-- ====================================
-- operation_receipts
-- Durable idempotency receipts for user or agent commands. The receipt,
-- effect and result are committed in one transaction; rows remain available
-- so a lost response can always be retried without repeating the effect.
-- ====================================
CREATE TABLE operation_receipts (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the externally visible operation id',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',

  principal_kind ENUM('user','agent') NOT NULL COMMENT 'Kind of actor that owns the operation key',
  principal_id INT UNSIGNED NOT NULL COMMENT 'Internal id of the user or agent that owns the operation key',
  operation_kind VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_bin NOT NULL COMMENT 'Stable operation family name',
  operation_key VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL COMMENT 'Opaque client retry key; compared case-sensitively',
  request_hash BINARY(32) NOT NULL COMMENT 'SHA-256 of the canonical operation request',
  target_kind VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_bin NOT NULL COMMENT 'Kind of entity targeted by the operation',
  target_public_id BINARY(16) NOT NULL COMMENT 'Public id of the target entity',
  result_json JSON NULL COMMENT 'Exact successful result returned on matching retries',
  completed_at DATETIME(3) NULL COMMENT 'Set only after the effect and result are ready to commit',

  updated_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3) COMMENT 'When the receipt last changed',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) COMMENT 'When this receipt row was created',

  UNIQUE KEY uniq_operation_receipts_public_id (public_id),
  UNIQUE KEY uniq_operation_receipts_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_operation_receipts_operation_key (workspace_id, principal_kind, principal_id, operation_kind, operation_key),
  KEY idx_operation_receipts_target (workspace_id, target_kind, target_public_id),

  CONSTRAINT fk_operation_receipts_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Durable command idempotency receipts';
