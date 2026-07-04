-- name: CreatePat :execlastid
-- Insert a new personal access token. Plain token is shown to the user once.
INSERT INTO personal_access_tokens (
  public_id,
  workspace_id,
  user_id,
  name,
  token_hash,
  token_prefix,
  scopes_json,
  expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindPatByHash :one
-- Resolve a PAT row from its SHA-256 hash for bearer auth.
SELECT
  pat.id,
  pat.public_id,
  pat.workspace_id,
  pat.user_id,
  pat.name,
  pat.token_prefix,
  pat.scopes_json,
  pat.expires_at,
  pat.last_used_at,
  pat.revoked_at,
  pat.enabled,
  pat.updated_at,
  pat.created_at
FROM personal_access_tokens pat
INNER JOIN users u ON u.id = pat.user_id AND u.enabled = TRUE
WHERE pat.token_hash = ?
  AND pat.enabled = TRUE
  AND pat.revoked_at IS NULL
LIMIT 1;

-- name: ListPatsForUser :many
-- List a user's PATs in a workspace, masked (no token_hash).
SELECT
  public_id,
  name,
  token_prefix,
  scopes_json,
  expires_at,
  last_used_at,
  updated_at,
  created_at,
  COUNT(*) OVER() AS total
FROM personal_access_tokens
WHERE workspace_id = ?
  AND user_id = ?
  AND enabled = TRUE
  AND revoked_at IS NULL
ORDER BY created_at DESC, public_id DESC
LIMIT ? OFFSET ?;

-- name: RevokePat :exec
-- Revoke a PAT (workspace + user scoped).
UPDATE personal_access_tokens
SET revoked_at = CURRENT_TIMESTAMP,
    enabled = FALSE
WHERE workspace_id = ?
  AND user_id = ?
  AND public_id = ?;
