-- name: FindUserForMcpToken :one
-- Resolve the owning user + workspace for an MCP bearer token by hash.
-- Returns internal ids for the auth middleware.
SELECT
  m.id          AS token_id,
  m.public_id   AS token_public_id,
  m.workspace_id,
  m.user_id,
  m.agent_id,
  m.scopes_json,
  m.expires_at,
  u.public_id   AS user_public_id,
  u.email,
  u.display_name
FROM mcp_tokens m
INNER JOIN users u ON u.id = m.user_id AND u.enabled = TRUE
WHERE m.token_hash = ?
  AND m.enabled = TRUE
  AND m.revoked_at IS NULL
LIMIT 1;

-- name: TouchMcpTokenLastUsed :execrows
-- Stamp an MCP token's last_used_at after a successful bearer auth.
-- Called with the internal token id resolved by FindUserForMcpToken.
UPDATE mcp_tokens
SET last_used_at = NOW()
WHERE id = ?;
