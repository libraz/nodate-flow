-- name: ListProviderModels :many
-- Every model (enabled and disabled) registered under a provider, with the
-- enabled-agent count for display only (unlocked). is_default is NULL-safe.
SELECT
  m.public_id,
  m.name,
  m.display_name,
  m.enabled,
  (m.name = COALESCE(p.default_model, '')) AS is_default,
  (SELECT COUNT(*) FROM ai_agents a WHERE a.model_id = m.id AND a.enabled = TRUE) AS agent_count,
  m.created_at
FROM ai_models m
INNER JOIN ai_providers p ON p.id = m.provider_id
WHERE m.workspace_id = ?
  AND m.provider_id = ?
ORDER BY m.created_at DESC, m.public_id DESC;

-- name: GetProviderModelForUpdate :one
-- Lock one model row of a provider (any enabled state) before a state change.
SELECT
  m.id,
  m.public_id,
  m.name,
  m.display_name,
  m.enabled,
  (m.name = COALESCE(p.default_model, '')) AS is_default
FROM ai_models m
INNER JOIN ai_providers p ON p.id = m.provider_id
WHERE m.workspace_id = ?
  AND m.provider_id = ?
  AND m.public_id = ?
LIMIT 1
FOR UPDATE;

-- name: GetAIModelByNameForUpdate :one
-- Lock the row that collides on (provider_id, name) after a duplicate-key
-- insert. Name matching is case-insensitive (column collation).
SELECT
  id,
  public_id,
  name,
  display_name,
  enabled
FROM ai_models
WHERE provider_id = ?
  AND name = ?
LIMIT 1
FOR UPDATE;

-- name: InsertAIModel :execlastid
-- Register a model under a provider. Duplicate (provider_id, name) raises a
-- duplicate-key error for the caller to handle.
INSERT INTO ai_models (
  public_id,
  workspace_id,
  provider_id,
  name,
  display_name,
  context_window,
  max_output_tokens,
  input_price_micro_usd_per_mtok,
  output_price_micro_usd_per_mtok,
  supports_tools,
  supports_vision,
  enabled
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, TRUE);

-- name: ReenableAIModel :execrows
-- Re-enable a disabled model, overwriting name and display_name. Rows-affected
-- is 0 when the row is already enabled or gone.
UPDATE ai_models
SET enabled = TRUE,
    name = ?,
    display_name = ?
WHERE id = ?
  AND enabled = FALSE;

-- name: SetAIModelEnabled :exec
-- Set the enabled flag on a model row the caller already holds locked.
UPDATE ai_models
SET enabled = ?
WHERE id = ?;

-- name: UpdateAIModelDisplayName :exec
-- Change the display label of a model row the caller already holds locked.
UPDATE ai_models
SET display_name = ?
WHERE id = ?;

-- name: CountEnabledModelsForWorkspace :one
-- Enabled models on enabled providers; the quantity capped per workspace.
-- Deliberately unlocked: the cap is a guideline, not an invariant.
SELECT COUNT(*)
FROM ai_models m
INNER JOIN ai_providers p ON p.id = m.provider_id AND p.enabled = TRUE
WHERE m.workspace_id = ?
  AND m.enabled = TRUE;

-- name: CountEnabledAgentsForModel :one
-- Enabled agents using a model. FOR SHARE forces a current read so a
-- concurrent agent create cannot be missed under REPEATABLE READ.
SELECT COUNT(*)
FROM ai_agents
WHERE model_id = ?
  AND enabled = TRUE
FOR SHARE;
