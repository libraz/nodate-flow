-- ====================================
-- embeddings
-- Generic vector index over arbitrary entities. entity_type narrows what
-- entity_id refers to (tasks, comments, ...).
--
-- NOTE: MySQL 8.4 Community does not implement the VECTOR type (HeatWave /
-- MySQL 9+ only). For Phase 1 we store the embedding as LONGBLOB containing
-- a serialized float32[1536] payload and defer the native vector index to a
-- later migration once MySQL 9 (or pgvector) is on the table.
-- ====================================
CREATE TABLE embeddings (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  model_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to ai_models.id (embedding model)',

  entity_type ENUM('task','comment','attachment','project','page') NOT NULL COMMENT 'Referenced entity kind',
  entity_id INT UNSIGNED NOT NULL COMMENT 'Internal id of the referenced entity row',
  content_hash CHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'SHA-256 hex of the embedded text (cache key)',
  dimensions SMALLINT UNSIGNED NOT NULL DEFAULT 1536 COMMENT 'Vector dimensionality',
  embedding LONGBLOB NOT NULL COMMENT 'Serialized float32[1536]; vector index deferred to MySQL 9 / pgvector',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_embeddings_public_id (public_id),
  UNIQUE KEY uniq_embeddings_entity_model (workspace_id, entity_type, entity_id, model_id),
  KEY idx_embeddings_workspace_id_entity_type (workspace_id, entity_type),
  KEY idx_embeddings_content_hash (content_hash),

  CONSTRAINT fk_embeddings_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_embeddings_model FOREIGN KEY (model_id) REFERENCES ai_models(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Vector embeddings over workspace entities';
