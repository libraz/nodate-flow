-- name: CreateSession :execlastid
-- Insert a new refresh-token session for a user.
INSERT INTO sessions (
  public_id,
  user_id,
  refresh_hash,
  user_agent,
  ip_address,
  expires_at
) VALUES (?, ?, ?, ?, ?, ?);

-- name: FindSessionByRefreshHash :one
-- Resolve a session from its SHA-256 refresh hash. Caller validates expiry.
SELECT
  id,
  public_id,
  user_id,
  refresh_hash,
  user_agent,
  ip_address,
  expires_at,
  revoked_at,
  last_used_at,
  enabled,
  updated_at,
  created_at
FROM sessions
WHERE refresh_hash = ?
  AND enabled = TRUE
  AND revoked_at IS NULL
LIMIT 1;

-- name: RevokeSession :exec
-- Mark a session as revoked. Workspace scoping does not apply (user-scoped).
UPDATE sessions
SET revoked_at = CURRENT_TIMESTAMP,
    enabled = FALSE
WHERE user_id = ?
  AND public_id = ?;

-- name: ListSessionsForUser :many
-- List a user's active sessions ordered by most recent first.
SELECT
  public_id,
  user_agent,
  ip_address,
  expires_at,
  last_used_at,
  updated_at,
  created_at,
  COUNT(*) OVER() AS total
FROM sessions
WHERE user_id = ?
  AND enabled = TRUE
  AND revoked_at IS NULL
ORDER BY created_at DESC, public_id DESC
LIMIT ? OFFSET ?;

-- name: FindSessionByPublicId :one
-- Resolve a session by its external public_id (UUID v7).
SELECT
  id,
  public_id,
  user_id,
  expires_at,
  revoked_at,
  enabled
FROM sessions
WHERE public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: RotateSessionRefreshHash :exec
-- Replace the refresh token hash and extend expiry on a refresh rotation.
UPDATE sessions
SET refresh_hash = ?,
    expires_at = ?
WHERE id = ?;
