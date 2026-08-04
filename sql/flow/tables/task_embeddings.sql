-- ====================================
-- task_embeddings
-- Per ADR 0003: native MySQL 9.x VECTOR storage for duplicate detection.
-- Vectors are L2-normalized before insert so cosine similarity reduces
-- to a dot product. (task_id, model) composite PK lets two models
-- coexist during a rolling re-embed; the duplicate-detection query
-- pins WHERE model = :current_model to avoid mixing vector spaces.
--
-- Internal plumbing only: never crosses the API boundary, so no
-- public_id column. workspace_id is denormalized from tasks for
-- workspace-scoped pruning / filtering without a JOIN; an explicit
-- FK to workspaces(id) ON DELETE CASCADE guarantees the denormalized
-- value cannot point at a removed workspace, even if a future writer
-- skips the JOIN through tasks.
-- ====================================
CREATE TABLE task_embeddings (
  task_id      INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Denormalized from tasks.workspace_id for scoped queries; FK guarantees consistency on workspace removal',
  model        VARCHAR(64)  NOT NULL COMMENT 'Embedding model key, e.g. mock-768',
  dim          SMALLINT UNSIGNED NOT NULL COMMENT 'Vector dimensionality (redundant with type today)',
  vector       VECTOR(768)  NOT NULL COMMENT 'L2-normalized embedding vector',
  content_hash CHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'SHA-256 hex of embedded text',
  embedded_at  DATETIME(3)  NOT NULL COMMENT 'Last embed time',

  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),

  PRIMARY KEY (task_id, model),
  KEY idx_task_embeddings_workspace_id (workspace_id, task_id),
  INDEX idx_task_embeddings_model_embedded_at (model, embedded_at),

  CONSTRAINT fk_task_embeddings_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_task_embeddings_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Task embedding vectors for duplicate detection (ADR 0003)';
