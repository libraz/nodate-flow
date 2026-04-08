-- name: AddProjectMember :execlastid
-- Add a user to a project (must already be a workspace member).
INSERT INTO project_members (
  public_id,
  workspace_id,
  project_id,
  user_id,
  role,
  added_at
) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListProjectMembers :many
-- List members of a project joined with user display fields.
SELECT
  pm.public_id,
  u.public_id AS user_public_id,
  u.email,
  u.display_name,
  u.avatar_url,
  pm.role,
  pm.added_at,
  pm.updated_at,
  pm.created_at,
  COUNT(*) OVER() AS total
FROM project_members pm
INNER JOIN users u ON u.id = pm.user_id AND u.enabled = TRUE
WHERE pm.workspace_id = ?
  AND pm.project_id = ?
  AND pm.enabled = TRUE
ORDER BY pm.created_at DESC, pm.public_id DESC
LIMIT ? OFFSET ?;

-- name: FindProjectMemberByUserId :one
-- Resolve a project membership by (project_id, user_id).
SELECT
  id,
  public_id,
  workspace_id,
  project_id,
  user_id,
  role,
  added_at,
  enabled,
  updated_at,
  created_at
FROM project_members
WHERE project_id = ?
  AND user_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: RemoveProjectMemberByUserId :exec
-- Soft-remove a project member keyed by user_id.
UPDATE project_members
SET enabled = FALSE
WHERE project_id = ?
  AND user_id = ?;

-- name: RemoveProjectMember :exec
-- Soft-remove a project member.
UPDATE project_members
SET enabled = FALSE
WHERE workspace_id = ?
  AND project_id = ?
  AND public_id = ?;
