-- ============================================================================
-- workspace_embedding_settings queries
-- ============================================================================

-- name: GetWorkspaceEmbeddingSettings :one
SELECT mode
FROM workspace_embedding_settings
WHERE workspace_id = ?
LIMIT 1;

-- name: UpsertWorkspaceEmbeddingSettings :exec
INSERT INTO workspace_embedding_settings (
  workspace_id,
  modified_by_user_id,
  mode
) VALUES (?, ?, ?)
ON DUPLICATE KEY UPDATE
  modified_by_user_id = VALUES(modified_by_user_id),
  mode = VALUES(mode);
