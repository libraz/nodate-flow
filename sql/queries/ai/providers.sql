-- ============================================================================
-- ai/providers.sql
-- WARNING: api_key_ciphertext is a write-only secret. The only query in this
-- file that reads it is FindProviderForDecrypt, which is reserved for the
-- apps/flow-api/internal/ai/providers/ package via golangci-lint depguard.
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

-- name: FindDefaultProviderIDForWorkspace :one
-- Return the internal id of the most recently created enabled provider
-- for a workspace. Used by the ai_invocations logger when the
-- orchestrator does not track which provider handled the call.
-- id is required: returned as FK value for ai_invocations.provider_id.
SELECT id
FROM ai_providers
WHERE workspace_id = ?
  AND enabled = TRUE
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: FindProviderForDecrypt :one
-- INTERNAL USE ONLY. Returns api_key_ciphertext for the providers package
-- to decrypt before calling the upstream LLM. Must NOT be called from
-- handlers, MCP tools, or any code outside apps/flow-api/internal/ai/providers/.
-- id is required: used internally by the providers package for logging/tracking.
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

-- name: GetEnabledProviderForWorkspace :one
-- Enabled provider lookup by public id. NEVER selects api_key_ciphertext.
SELECT
  id,
  public_id,
  kind,
  name,
  default_model
FROM ai_providers
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: LockEnabledProviderForUpdate :one
-- Lock an enabled provider row; serializes against agent-side FOR SHARE reads.
SELECT id
FROM ai_providers
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
LIMIT 1
FOR UPDATE;

-- name: SetProviderDefaultModel :exec
-- Point the provider's default_model at a model name.
UPDATE ai_providers
SET default_model = ?
WHERE id = ?;

-- name: SetProviderDefaultModelIfNull :exec
-- Set default_model only while it is still unset.
UPDATE ai_providers
SET default_model = ?
WHERE id = ?
  AND default_model IS NULL;

-- name: CountEnabledAgentsForProvider :one
-- Enabled agents using any model of a provider. FOR SHARE forces a current
-- read so a concurrent agent create cannot be missed under REPEATABLE READ.
SELECT COUNT(*)
FROM ai_agents a
INNER JOIN ai_models m ON m.id = a.model_id
WHERE m.provider_id = ?
  AND a.enabled = TRUE
FOR SHARE;

-- name: UpdateProviderKey :execrows
-- Rotate a provider's API key. Caller passes new ciphertext + prefix + suffix.
-- Returns rows-affected so the handler can detect a not-found / wrong-workspace
-- target (0 rows) instead of reporting a false success.
UPDATE ai_providers
SET api_key_ciphertext = ?,
    api_key_prefix = ?,
    api_key_suffix = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: DeleteProvider :execrows
-- Soft-delete a provider. Returns rows-affected so the handler can detect a
-- not-found / wrong-workspace target (0 rows) instead of reporting a false
-- success.
UPDATE ai_providers
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;
