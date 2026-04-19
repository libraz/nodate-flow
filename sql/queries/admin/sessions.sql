-- name: AdminListUserSessions :many
-- List all sessions for a user by their internal user_id.
SELECT
  s.public_id,
  s.user_agent,
  s.ip_address,
  s.expires_at,
  s.revoked_at,
  s.last_used_at,
  s.enabled,
  s.created_at,
  COUNT(*) OVER() AS total
FROM sessions s
WHERE s.user_id = ?
ORDER BY s.created_at DESC, s.public_id DESC
LIMIT ? OFFSET ?;

-- name: AdminRevokeSession :exec
-- Revoke any session by its public_id (admin override, no user scoping).
UPDATE sessions
SET revoked_at = CURRENT_TIMESTAMP,
    enabled = FALSE
WHERE public_id = ?
  AND enabled = TRUE
  AND revoked_at IS NULL;

-- name: AdminFindUserIdByPublicId :one
-- Resolve internal user_id from public_id for admin session lookup.
SELECT id FROM users WHERE public_id = ?;
