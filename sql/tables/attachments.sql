-- ====================================
-- attachments
-- Files uploaded against a task. The actual blob lives in object storage
-- under storage_key; this row is the metadata index.
-- ====================================
CREATE TABLE attachments (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NULL COMMENT 'Internal FK to tasks.id; nullable so audit-trail attachments survive task deletion (FK SET NULL)',
  uploader_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id (uploader)',

  filename VARCHAR(255) NOT NULL COMMENT 'Original filename',
  content_type VARCHAR(127) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'MIME type',
  byte_size BIGINT UNSIGNED NOT NULL COMMENT 'Size in bytes',
  storage_key VARCHAR(512) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Object storage key (e.g., s3 key)',
  checksum_sha256 CHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'SHA-256 hex of file contents',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_attachments_public_id (public_id),
  UNIQUE KEY uniq_attachments_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_attachments_storage_key (storage_key),
  KEY idx_attachments_workspace_id_task_id (workspace_id, task_id),
  KEY idx_attachments_workspace_id_uploader_id (workspace_id, uploader_id),

  CONSTRAINT fk_attachments_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_attachments_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL,
  CONSTRAINT fk_attachments_uploader FOREIGN KEY (uploader_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Task file attachments metadata';
