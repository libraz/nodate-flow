-- name: CreateWorkspaceMember :execlastid
-- Add a user to a workspace with the given role.
INSERT INTO workspace_members (
  public_id,
  workspace_id,
  user_id,
  role,
  invited_by_user_id,
  invited_at,
  joined_at
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListWorkspaceMembers :many
-- List members of a workspace via v_workspace_members.
SELECT
  v.public_id,
  v.user_public_id,
  v.email,
  v.display_name,
  v.avatar_url,
  v.role,
  v.invited_at,
  v.joined_at,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_workspace_members v
WHERE v.workspace_id = ?
ORDER BY v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: RemoveWorkspaceMember :exec
-- Soft-remove a member from a workspace.
UPDATE workspace_members
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;

-- name: FindWorkspaceMemberByUserId :one
-- Resolve a workspace membership by (workspace_id, user_id).
SELECT
  id,
  public_id,
  workspace_id,
  user_id,
  role,
  invited_by_user_id,
  invited_at,
  joined_at,
  enabled,
  updated_at,
  created_at
FROM workspace_members
WHERE workspace_id = ?
  AND user_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: UpdateMemberRoleByUserId :exec
-- Change a member's role keyed by user_id.
UPDATE workspace_members
SET role = ?
WHERE workspace_id = ?
  AND user_id = ?
  AND enabled = TRUE;

-- name: RemoveWorkspaceMemberByUserId :exec
-- Soft-remove a member keyed by user_id.
UPDATE workspace_members
SET enabled = FALSE
WHERE workspace_id = ?
  AND user_id = ?;

-- name: CheckWorkspaceMemberExists :one
-- Verify that a user is an enabled member of a workspace. Returns 1 if
-- the membership exists, sql.ErrNoRows otherwise.
SELECT 1 AS ok FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE
LIMIT 1;

-- name: GetWorkspaceMemberRole :one
-- Return the role string for an enabled workspace member. Returns
-- sql.ErrNoRows when the user is not a member.
SELECT role FROM workspace_members
WHERE workspace_id = ? AND user_id = ? AND enabled = TRUE
LIMIT 1;

-- name: UpdateMemberRole :exec
-- Change a member's role.
UPDATE workspace_members
SET role = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;
