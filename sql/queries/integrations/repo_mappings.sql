-- name: GetRepoMappingByRepoID :one
-- Look up the workspace mapping for a GitHub repository by its numeric
-- repo ID. Used by the webhook handler to route incoming events to the
-- correct workspace. Only returns enabled rows.
SELECT
  id,
  public_id,
  workspace_id,
  integration_id,
  repo_full_name,
  repo_id,
  default_project_id,
  is_sync_issues,
  is_sync_pull_requests
FROM repo_workspace_mappings
WHERE repo_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: ListRepoMappingsForWorkspace :many
-- List all active repository mappings for a workspace. Returns metadata
-- only (no internal IDs leak through the view layer).
SELECT
  public_id,
  repo_full_name,
  repo_id,
  default_project_id,
  is_sync_issues,
  is_sync_pull_requests,
  created_at
FROM repo_workspace_mappings
WHERE workspace_id = ?
  AND enabled = TRUE
ORDER BY repo_full_name ASC, id ASC;

-- name: CreateRepoMapping :execlastid
-- Insert a new repository-to-workspace mapping.
INSERT INTO repo_workspace_mappings (
  public_id,
  workspace_id,
  integration_id,
  repo_full_name,
  repo_id,
  default_project_id,
  is_sync_issues,
  is_sync_pull_requests
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteRepoMapping :exec
-- Soft-delete a mapping by setting enabled = FALSE. Scoped to the
-- workspace and identified by public_id.
UPDATE repo_workspace_mappings
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;
