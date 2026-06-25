-- name: CreateWorkspaceInvite :execlastid
-- Insert a new invite link for a workspace.
INSERT INTO workspace_invites (
  public_id, workspace_id, token_hash, role,
  created_by_user_id, max_uses, expires_at, label
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindWorkspaceInviteByTokenHash :one
-- Look up an active invite by its SHA-256 token hash.
-- Caller must validate expiry and use_count.
SELECT
  id, public_id, workspace_id, token_hash, role,
  created_by_user_id, max_uses, use_count,
  expires_at, label, enabled, created_at
FROM workspace_invites
WHERE token_hash = ?
  AND enabled = TRUE
LIMIT 1;

-- name: ListWorkspaceInvites :many
-- List active invites for a workspace, most recent first.
SELECT
  wi.public_id, wi.role, wi.max_uses, wi.use_count,
  wi.expires_at, wi.label, wi.created_at,
  u.display_name AS created_by_name,
  COUNT(*) OVER() AS total
FROM workspace_invites wi
JOIN users u ON u.id = wi.created_by_user_id
WHERE wi.workspace_id = ?
  AND wi.enabled = TRUE
ORDER BY wi.created_at DESC, wi.public_id DESC
LIMIT ? OFFSET ?;

-- name: IncrementInviteUseCount :execrows
-- Atomically bump the use counter, but only while the invite still has
-- capacity. max_uses IS NULL means unlimited. The conditional WHERE makes
-- the check-and-increment a single statement so concurrent redemptions
-- can never push use_count past max_uses (TOCTOU-safe). Returns the number
-- of affected rows: 0 means the invite was already exhausted.
UPDATE workspace_invites
SET use_count = use_count + 1
WHERE id = ?
  AND enabled = TRUE
  AND (max_uses IS NULL OR use_count < max_uses);

-- name: RevokeWorkspaceInvite :exec
-- Disable an invite link (soft delete).
UPDATE workspace_invites
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;

-- name: FindWorkspaceInviteWorkspaceName :one
-- Get workspace name for invite info endpoint (public).
SELECT w.name, w.public_id AS workspace_public_id
FROM workspace_invites wi
JOIN workspaces w ON w.id = wi.workspace_id
WHERE wi.token_hash = ?
  AND wi.enabled = TRUE
LIMIT 1;
