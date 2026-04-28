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

-- name: MarkMagicLinkUsed :execrows
-- Atomically stamp used_at on a magic link token. The WHERE clause
-- includes used_at IS NULL so two concurrent verify requests racing on
-- the same token can never both succeed: exactly one UPDATE will match
-- and the loser sees zero affected rows. Callers MUST inspect
-- RowsAffected and treat 0 as "already consumed" (return ALREADY_USED).
UPDATE magic_link_tokens
SET used_at = CURRENT_TIMESTAMP
WHERE id = ?
  AND used_at IS NULL;

-- name: CleanupExpiredMagicLinks :exec
-- Delete tokens that are either expired-and-used or expired-and-unused, for periodic cleanup.
DELETE FROM magic_link_tokens
WHERE expires_at < CURRENT_TIMESTAMP;
