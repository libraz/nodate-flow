-- name: CreateMagicLinkToken :execlastid
-- Insert a new magic link token for passwordless login.
INSERT INTO magic_link_tokens (
  public_id,
  user_id,
  token_hash,
  expires_at,
  ip_address
) VALUES (?, ?, ?, ?, ?);

-- name: FindMagicLinkByTokenHash :one
-- Resolve a magic link token by its SHA-256 hash. Caller validates expiry.
SELECT
  id,
  public_id,
  user_id,
  token_hash,
  expires_at,
  used_at,
  ip_address,
  created_at
FROM magic_link_tokens
WHERE token_hash = ?
  AND enabled = TRUE
LIMIT 1;

-- name: MarkMagicLinkUsed :exec
-- Stamp used_at on a magic link token after successful verification.
UPDATE magic_link_tokens
SET used_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: CleanupExpiredMagicLinks :exec
-- Delete tokens that are either expired-and-used or expired-and-unused, for periodic cleanup.
DELETE FROM magic_link_tokens
WHERE expires_at < CURRENT_TIMESTAMP;
