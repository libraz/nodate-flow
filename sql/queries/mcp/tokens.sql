-- name: CreateMcpToken :execlastid
-- Insert a new MCP token. Plain token is shown to the user once.
INSERT INTO mcp_tokens (
  public_id,
  workspace_id,
  user_id,
  agent_id,
  name,
  token_hash,
  token_prefix,
  scopes_json,
  expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindAgentInternalIDByPublicID :one
-- Resolve an ai_agents row's internal id by public_id, workspace-scoped.
SELECT id FROM ai_agents
WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE
LIMIT 1;

-- name: FindMcpTokenByHash :one
-- Resolve an MCP token by its SHA-256 hash for bearer auth.
SELECT
  id,
  public_id,
  workspace_id,
  user_id,
  name,
  token_prefix,
  scopes_json,
  expires_at,
  last_used_at,
  revoked_at,
  enabled,
  updated_at,
  created_at
FROM mcp_tokens
WHERE token_hash = ?
  AND enabled = TRUE
  AND revoked_at IS NULL
LIMIT 1;

-- name: ListMcpTokensForUser :many
-- List a user's MCP tokens in a workspace, masked.
SELECT
  m.public_id,
  m.name,
  m.token_prefix,
  m.scopes_json,
  a.public_id AS agent_public_id,
  m.expires_at,
  m.last_used_at,
  m.updated_at,
  m.created_at,
  COUNT(*) OVER() AS total
FROM mcp_tokens m
LEFT JOIN ai_agents a ON a.id = m.agent_id
WHERE m.workspace_id = ?
  AND m.user_id = ?
  AND m.enabled = TRUE
  AND m.revoked_at IS NULL
ORDER BY m.created_at DESC, m.public_id DESC
LIMIT ? OFFSET ?;

-- name: RevokeMcpToken :exec
-- Revoke an MCP token (workspace + user scoped).
UPDATE mcp_tokens
SET revoked_at = CURRENT_TIMESTAMP,
    enabled = FALSE
WHERE workspace_id = ?
  AND user_id = ?
  AND public_id = ?;
