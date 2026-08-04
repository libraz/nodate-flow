-- ====================================
-- attachments
-- Files uploaded against a task. The actual blob and its content metadata
-- (sha256 / byte_size / content_type / storage_key) live in storage_objects;
-- this row is the per-task reference (filename, uploader, sort order). The
-- same blob uploaded twice within a workspace shares one storage_objects
-- row and bumps its ref_count.
-- ====================================
CREATE TABLE attachments (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NULL COMMENT 'Internal FK to tasks.id; nullable so audit-trail attachments survive task deletion (FK SET NULL)',
  uploader_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id (uploader)',
  storage_object_id INT UNSIGNED NOT NULL COMMENT 'FK to storage_objects.id; holds the actual blob metadata (sha256, byte_size, content_type, storage_key)',

  filename VARCHAR(512) NOT NULL COMMENT 'Original filename as supplied by the uploader; widened to 512 to safely hold multibyte paths',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_attachments_public_id (public_id),
  UNIQUE KEY uniq_attachments_workspace_public_id (workspace_id, public_id),
  KEY idx_attachments_workspace_id_task_id (workspace_id, task_id),
  KEY idx_attachments_workspace_id_uploader_id (workspace_id, uploader_id),
  KEY idx_attachments_storage_object (storage_object_id),

  CONSTRAINT fk_attachments_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_attachments_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL,
  CONSTRAINT fk_attachments_uploader FOREIGN KEY (uploader_id) REFERENCES users(id) ON DELETE CASCADE,
  -- RESTRICT: attachments must be deleted (and ref_count decremented) before
  -- the underlying storage_objects row may be removed by the GC sweeper.
  CONSTRAINT fk_attachments_storage_object FOREIGN KEY (storage_object_id) REFERENCES storage_objects(id) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Task file attachments metadata';
