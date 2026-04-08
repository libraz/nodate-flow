-- ============================================================================
-- ai/providers.sql
-- WARNING: api_key_ciphertext is a write-only secret. The only query in this
-- file that reads it is FindProviderForDecrypt, which is reserved for the
-- apps/api/internal/ai/providers/ package via golangci-lint depguard.
-- All list / read queries MUST exclude api_key_ciphertext.
-- ============================================================================

-- name: CreateProvider :execlastid
-- Insert a new LLM provider with encrypted API key.
INSERT INTO ai_providers (
  public_id,
  workspace_id,
  kind,
  name,
  base_url,
  api_key_ciphertext,
  api_key_prefix,
  api_key_suffix,
  default_model
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListProvidersForWorkspace :many
-- Workspace provider list. NEVER selects api_key_ciphertext.
SELECT
  public_id,
  kind,
  name,
  base_url,
  api_key_prefix,
  api_key_suffix,
  default_model,
  updated_at,
  created_at,
  COUNT(*) OVER() AS total
FROM ai_providers
WHERE workspace_id = ?
  AND enabled = TRUE
ORDER BY created_at DESC, public_id DESC
LIMIT ? OFFSET ?;

-- name: FindProviderForDecrypt :one
-- INTERNAL USE ONLY. Returns api_key_ciphertext for the providers package
-- to decrypt before calling the upstream LLM. Must NOT be called from
-- handlers, MCP tools, or any code outside apps/api/internal/ai/providers/.
SELECT
  id,
  public_id,
  workspace_id,
  kind,
  name,
  base_url,
  api_key_ciphertext,
  api_key_prefix,
  api_key_suffix,
  default_model,
  enabled,
  updated_at,
  created_at
FROM ai_providers
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: UpdateProviderKey :exec
-- Rotate a provider's API key. Caller passes new ciphertext + prefix + suffix.
UPDATE ai_providers
SET api_key_ciphertext = ?,
    api_key_prefix = ?,
    api_key_suffix = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: DeleteProvider :exec
-- Soft-delete a provider.
UPDATE ai_providers
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;
