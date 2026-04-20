-- name: CountActiveUsers :one
-- Count all active (non-disabled) users.
SELECT COUNT(*) AS total FROM users WHERE enabled = TRUE;

-- name: CountActiveWorkspaces :one
-- Count all active (non-disabled) workspaces.
SELECT COUNT(*) AS total FROM workspaces WHERE enabled = TRUE;
