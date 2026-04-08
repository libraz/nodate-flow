-- ====================================
-- task_embeddings
-- Per ADR 0003: native MySQL 9.x VECTOR storage for duplicate detection.
-- Vectors are L2-normalized before insert so cosine similarity reduces
-- to a dot product. (task_id, model) composite PK lets two models
-- coexist during a rolling re-embed; the duplicate-detection query
-- pins WHERE model = :current_model to avoid mixing vector spaces.
--
-- Internal plumbing only: never crosses the API boundary, so no
-- public_id / workspace_id columns. Workspace scoping is reached via
-- the FK to tasks(id) (ON DELETE CASCADE).
-- ====================================
CREATE TABLE task_embeddings (
  task_id      INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id',
  model        VARCHAR(64)  NOT NULL COMMENT 'Embedding model key, e.g. mock-768',
  dim          SMALLINT UNSIGNED NOT NULL COMMENT 'Vector dimensionality (redundant with type today)',
  vector       VECTOR(768)  NOT NULL COMMENT 'L2-normalized embedding vector',
  content_hash CHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'SHA-256 hex of embedded text',
  embedded_at  DATETIME(3)  NOT NULL COMMENT 'Last embed time',

  PRIMARY KEY (task_id, model),
  INDEX idx_task_embeddings_model_embedded_at (model, embedded_at),

  CONSTRAINT fk_task_embeddings_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Task embedding vectors for duplicate detection (ADR 0003)';
