-- ============================================================================
-- task_embeddings queries (ADR 0003)
--
-- Internal plumbing: task_embeddings has no public_id of its own; workspace
-- scoping is reached via the denormalized workspace_id column (FK-guarded
-- against tasks.workspace_id). Every query below filters through that
-- column so the workspace boundary still holds without a JOIN through tasks.
--
-- VECTOR columns are read/written as []byte. The Go embedding client
-- L2-normalizes vectors before INSERT and serializes them with
-- STRING_TO_VECTOR / the native binary protocol. Cosine similarity is
-- computed in Go (dot product on normalized vectors), because MySQL 9.6
-- Community Edition does not expose VEC_DISTANCE_COSINE (HeatWave-only).
-- The query layer
-- therefore returns candidate rows and the application does the ranking.
-- ============================================================================

-- name: UpsertTaskEmbedding :exec
-- Insert or replace the embedding row for (task_id, model). The caller is
-- responsible for L2-normalizing `vector` before calling this query. The
-- content_hash lets callers skip re-embedding when the input text is
-- unchanged. workspace_id is denormalized from tasks for scoped pruning;
-- callers MUST pass the task's owning workspace.
INSERT INTO task_embeddings (task_id, workspace_id, model, dim, vector, content_hash, embedded_at)
VALUES (?, ?, ?, ?, STRING_TO_VECTOR(?), ?, NOW(3))
ON DUPLICATE KEY UPDATE
  dim = VALUES(dim),
  vector = VALUES(vector),
  content_hash = VALUES(content_hash),
  embedded_at = VALUES(embedded_at);

-- name: GetTaskEmbedding :one
-- Fetch the embedding row for a single (task_id, model) pair. Returns
-- sql.ErrNoRows if the task has never been embedded with the given model.
SELECT task_id, model, dim, VECTOR_TO_STRING(vector) AS vector, content_hash, embedded_at
FROM task_embeddings
WHERE task_id = ?
  AND model = ?
LIMIT 1;

-- name: ListStaleTaskEmbeddings :many
-- List tasks whose embedding is missing or older than tasks.updated_at for
-- the given (workspace_id, model). Used by the background re-embed worker.
-- LEFT JOIN so tasks with no row at all are returned as "stale".
SELECT
  t.id,
  t.public_id,
  t.title,
  t.description,
  UNIX_TIMESTAMP(t.updated_at) AS updated_at_unix
FROM tasks t
LEFT JOIN task_embeddings te
  ON te.task_id = t.id
  AND te.model = ?
WHERE t.workspace_id = ?
  AND t.enabled = TRUE
  AND (te.task_id IS NULL OR te.embedded_at < t.updated_at)
ORDER BY t.updated_at DESC, t.id DESC
LIMIT ?;

-- name: ListCandidateTaskEmbeddings :many
-- Return all task_embeddings for (workspace_id, model), excluding a given
-- task_id (so self-similarity is filtered out). Cosine similarity is
-- computed in Go because MySQL 9.6 Community does not expose
-- VEC_DISTANCE_COSINE; the caller dot-products the L2-normalized vectors
-- and applies the duplicate_threshold_high / low cutoffs from ai_settings.
SELECT
  t.id,
  t.public_id,
  t.title,
  VECTOR_TO_STRING(te.vector) AS vector
FROM task_embeddings te
INNER JOIN tasks t
  ON t.id = te.task_id
  AND t.enabled = TRUE
WHERE t.workspace_id = ?
  AND te.model = ?
  AND te.task_id <> ?
ORDER BY t.id DESC
LIMIT ?;

-- name: DeleteTaskEmbeddingsForTask :exec
-- Remove every embedding row for a task across all models. ON DELETE
-- CASCADE already handles task deletion; this query is for the
-- re-embed-on-edit flow when a workspace switches to a different model.
DELETE FROM task_embeddings
WHERE task_id = ?;
