-- name: AddProjectMember :execlastid
-- Add a user to a project (must already be a workspace member).
--
-- uniq_project_members_project_id_user_id covers removed members too, so
-- a revoked row keeps holding the (project, user) pair and a plain
-- insert collides with it: without the revival below, anyone removed
-- from a project could never be added back. Callers check for a live
-- membership first, so what reaches here is either new or revoked.
--
-- Re-adding states the grant afresh: the role is the one being asked
-- for now, added_at records this joining, and the row adopts the
-- public_id the caller has already reported to the client.
INSERT INTO project_members (
  public_id,
  workspace_id,
  project_id,
  user_id,
  role,
  added_at
) VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  public_id = VALUES(public_id),
  role      = VALUES(role),
  added_at  = VALUES(added_at),
  enabled   = TRUE;

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

-- name: ListProjectPublicIdsForUserInWorkspace :many
-- List the public_ids of every enabled project in a workspace for which
-- the given user has an enabled project_members row. Used by the
-- per-project ACL filter on GET /workspaces/{wsId}/projects so non-member
-- workspace members do not enumerate projects they cannot open.
SELECT p.public_id
FROM project_members pm
JOIN projects p ON p.id = pm.project_id
WHERE pm.workspace_id = ?
  AND pm.user_id = ?
  AND pm.enabled = TRUE
  AND p.enabled = TRUE;

-- name: RemoveProjectMemberByUserId :exec
-- Soft-remove a project member keyed by user_id.
UPDATE project_members
SET enabled = FALSE
WHERE project_id = ?
  AND user_id = ?;

-- name: RemoveProjectMember :execrows
-- Soft-remove a project member.
UPDATE project_members
SET enabled = FALSE
WHERE workspace_id = ?
  AND project_id = ?
  AND public_id = ?
  AND enabled = TRUE;
