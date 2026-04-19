-- name: AdminListWorkspaces :many
-- Paginated workspace list for instance admin panel.
-- search: pass '' to skip, otherwise matches name or slug.
-- filter_enabled: pass NULL to skip, otherwise filters by enabled flag.
SELECT
  w.public_id,
  w.slug,
  w.name,
  w.description,
  w.icon_url,
  w.enabled,
  w.created_at,
  w.updated_at,
  COUNT(DISTINCT wm.id) AS member_count,
  COUNT(*) OVER() AS total
FROM workspaces w
LEFT JOIN workspace_members wm ON wm.workspace_id = w.id AND wm.enabled = TRUE
WHERE (? = '' OR w.name LIKE CONCAT('%', ?, '%') OR w.slug LIKE CONCAT('%', ?, '%'))
  AND (? IS NULL OR w.enabled = ?)
GROUP BY w.id
ORDER BY w.created_at DESC, w.public_id DESC
LIMIT ? OFFSET ?;

-- name: AdminGetWorkspace :one
-- Find a single workspace by public_id for admin detail view.
SELECT
  w.public_id,
  w.slug,
  w.name,
  w.description,
  w.icon_url,
  w.enabled,
  w.created_at,
  w.updated_at,
  COUNT(DISTINCT wm.id) AS member_count,
  COUNT(DISTINCT p.id) AS project_count
FROM workspaces w
LEFT JOIN workspace_members wm ON wm.workspace_id = w.id AND wm.enabled = TRUE
LEFT JOIN projects p ON p.workspace_id = w.id AND p.enabled = TRUE
WHERE w.public_id = ?
GROUP BY w.id;

-- name: AdminSuspendWorkspace :exec
-- Disable a workspace (soft-delete).
UPDATE workspaces SET enabled = FALSE WHERE public_id = ? AND enabled = TRUE;

-- name: AdminEnableWorkspace :exec
-- Re-enable a previously suspended workspace.
UPDATE workspaces SET enabled = TRUE WHERE public_id = ? AND enabled = FALSE;
