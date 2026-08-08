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
-- id is required: used by RotateSessionRefreshHash (WHERE id = ?).
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

-- name: FindAnySessionByRefreshHash :one
-- Resolve a session from its SHA-256 refresh hash regardless of its
-- revoked / enabled state. Used by the refresh-reuse detector: a hash
-- that matches a row which is already revoked or disabled is evidence
-- that a previously-rotated (and thus invalidated) refresh token was
-- replayed, so the whole session family must be torn down.
SELECT
  id,
  public_id,
  user_id,
  refresh_hash,
  expires_at,
  revoked_at,
  last_used_at,
  enabled,
  created_at
FROM sessions
WHERE refresh_hash = ?
LIMIT 1;

-- name: FindSessionByRotatedFromHash :one
-- Resolve the session that superseded the given refresh token.
--
-- This is the reuse signal. Rotation overwrites refresh_hash in place,
-- so a token that has been rotated away matches no session's current
-- hash and is indistinguishable from one that was never issued —
-- which is why looking for it among revoked rows found nothing, and
-- found the wrong thing when a session had merely been signed out. A
-- hash recorded here, by contrast, can only have got there by being
-- replaced during a rotation of this very session, so presenting it
-- means someone still holds a token the legitimate client gave up.
SELECT
  id,
  public_id,
  user_id,
  refresh_hash,
  expires_at,
  revoked_at,
  last_used_at,
  enabled,
  created_at
FROM sessions
WHERE rotated_from_hash = ?
LIMIT 1;

-- name: RevokeSession :execrows
-- Mark a session as revoked. Workspace scoping does not apply (user-scoped).
UPDATE sessions
SET revoked_at = CURRENT_TIMESTAMP,
    enabled = FALSE
WHERE user_id = ?
  AND public_id = ?
  AND enabled = TRUE
  AND revoked_at IS NULL;

-- name: RevokeAllSessionsForUser :exec
-- Revoke every active session for a user. Used by the refresh-reuse
-- detector to tear down the entire session family when a rotated /
-- revoked refresh token is replayed.
UPDATE sessions
SET revoked_at = CURRENT_TIMESTAMP,
    enabled = FALSE
WHERE user_id = ?
  AND enabled = TRUE
  AND revoked_at IS NULL;

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
-- id is required: used internally for session operations.
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

-- name: RevokeAllSessionsForUserExcept :execrows
-- Revoke every active session for a user except one identified by public_id.
-- Used by "sign out of all other devices" in /settings/security.
UPDATE sessions
SET revoked_at = CURRENT_TIMESTAMP,
    enabled = FALSE
WHERE user_id = ?
  AND enabled = TRUE
  AND revoked_at IS NULL
  AND public_id <> ?;

-- name: RotateSessionRefreshHash :exec
-- Replace the refresh token hash, extend expiry, and record last usage on a refresh rotation.
UPDATE sessions
SET refresh_hash = ?,
    expires_at = ?,
    last_used_at = CURRENT_TIMESTAMP
WHERE id = ?;
