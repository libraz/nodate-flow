-- ====================================
-- calendar_event_attachments
-- Files uploaded against a calendar event. Actual blobs live in object
-- storage under storage_key; this table is the metadata index.
-- Uses soft-delete via enabled flag.
-- ====================================
CREATE TABLE calendar_event_attachments (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  event_id INT UNSIGNED NULL COMMENT 'Internal FK to calendar_events.id; nullable so audit-trail attachments survive event hard-delete (FK SET NULL)',
  uploader_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id (uploader)',

  filename VARCHAR(255) NOT NULL COMMENT 'Original filename',
  content_type VARCHAR(127) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL DEFAULT 'application/octet-stream' COMMENT 'MIME type',
  byte_size BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Size in bytes',
  storage_key VARCHAR(512) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Object storage key',
  checksum_sha256 CHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'SHA-256 hex of file contents',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag (soft delete)',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_calendar_event_attachments_public_id (public_id),
  UNIQUE KEY uniq_calendar_event_attachments_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_calendar_event_attachments_storage_key (storage_key),
  KEY idx_calendar_event_attachments_event (event_id, enabled),
  KEY idx_calendar_event_attachments_workspace_uploader (workspace_id, uploader_id),

  CONSTRAINT fk_calendar_event_attachments_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_calendar_event_attachments_event FOREIGN KEY (event_id) REFERENCES calendar_events(id) ON DELETE SET NULL,
  CONSTRAINT fk_calendar_event_attachments_uploader FOREIGN KEY (uploader_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Calendar event file attachments';
