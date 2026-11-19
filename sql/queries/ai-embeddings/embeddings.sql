-- ============================================================================
-- task_embeddings queries
--
-- Internal plumbing: task_embeddings has no public_id of its own; workspace
-- scoping is reached via the denormalized workspace_id column (FK-guarded
-- against tasks.workspace_id). Every query below filters through that
-- column so the workspace boundary still holds without a JOIN through tasks.
--
-- VECTOR columns are read/written as []byte. The Go embedding client
-- L2-normalizes vectors before INSERT and serializes them with
-- STRING_TO_VECTOR / the native binary protocol. Cosine similarity is
-- computed in Go as a dot product on normalized vectors. MySQL Community
-- Edition does not include the vector DISTANCE function.
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
  -- Title and description are on the wire, so the row set is the one the
  -- actor may read. Elevated roles skip the check.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR t.visibility = 'public'
    OR (t.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = t.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (t.visibility = 'private' AND (
      t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = t.id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  )
ORDER BY t.updated_at DESC, t.id DESC
LIMIT ?;

-- name: ListCandidateTaskEmbeddings :many
-- Return the newest task_embeddings for (workspace_id, model), up to the
-- caller's LIMIT by descending task id, excluding a given task_id (so
-- self-similarity is filtered out). Tasks older than that window are not
-- candidates. The Go caller computes cosine similarity from the
-- L2-normalized vectors and applies the cutoffs from ai_settings.
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
  -- Candidate titles reach the caller as duplicate/relation suggestions
  -- and as LLM prompt material, so the candidate pool is the set the
  -- actor may read. Elevated roles skip the check.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR t.visibility = 'public'
    OR (t.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = t.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (t.visibility = 'private' AND (
      t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = t.id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  )
ORDER BY t.id DESC
LIMIT ?;

-- name: GetProposalCacheWorkspaceAccess :one
-- Current authority used by prompt snapshots and proposal-cache hits. An
-- absent or disabled workspace membership is intentionally no row.
SELECT CAST(wm.role AS CHAR) AS role
FROM workspaces w
JOIN workspace_members wm
  ON wm.workspace_id = w.id
  AND wm.enabled = TRUE
WHERE w.id = ?
  AND w.enabled = TRUE
  AND wm.user_id = ?
LIMIT 1;

-- name: ListProposalCacheTaskInputs :many
-- Reload every task quoted by a cached proposal through the current
-- Layer-4 ACL, returning the prompt-relevant fields used to fingerprint the
-- cache snapshot. Missing, disabled, moved, or unreadable tasks are absent;
-- callers require one row for every public id before serving a cache hit.
WITH cache_scope AS (
  SELECT
    w.id AS workspace_id,
    (wm.role IN ('owner', 'admin')) AS is_elevated
  FROM workspaces w
  JOIN workspace_members wm
    ON wm.workspace_id = w.id
    AND wm.enabled = TRUE
  WHERE w.id = sqlc.arg('workspace_id')
    AND w.enabled = TRUE
    AND wm.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
)
SELECT
  t.id,
  t.public_id,
  t.title,
  t.description,
  t.derived_state,
  COALESCE(BIN_TO_UUID(u.public_id, 0), '') AS assignee_public_id,
  u.display_name AS assignee_name
FROM cache_scope scope
JOIN tasks t ON t.workspace_id = scope.workspace_id
JOIN projects p
  ON p.id = t.project_id
  AND p.workspace_id = t.workspace_id
  AND p.enabled = TRUE
-- The primary assignee rule of v_task_list_all: the first assignee in
-- display order, with no name when that actor is an agent or disabled.
LEFT JOIN task_actors pa
  ON pa.id = (
    SELECT ta.id
    FROM task_actors ta
    WHERE ta.task_id = t.id
      AND ta.enabled = TRUE
      AND ta.role = 'assignee'
    ORDER BY ta.sort_weight ASC, ta.id ASC
    LIMIT 1
  )
LEFT JOIN users u ON u.id = pa.user_id AND u.enabled = TRUE
WHERE t.enabled = TRUE
  AND t.public_id IN (sqlc.slice('public_ids'))
  -- The same literal task-visibility predicate is used by other task reads.
  -- Elevated roles derive from the current membership above, never a bind
  -- supplied by the request middleware.
  AND (
    t.visibility = 'public'
    OR (t.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = t.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (t.visibility = 'private' AND (
      t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = t.id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
    OR scope.is_elevated = 1
  )
ORDER BY t.public_id;

-- name: DeleteTaskEmbeddingsForTask :exec
-- Remove every embedding row for a task across all models. ON DELETE
-- CASCADE already handles task deletion; this query is for the
-- re-embed-on-edit flow when a workspace switches to a different model.
--
-- affected-rows: not-applicable — it clears whatever vectors a task
-- carries before new ones are written. A task nobody has embedded yet
-- holds none, which is already the state this is asked to produce.
DELETE FROM task_embeddings
WHERE task_id = ?;
