-- name: ListModelsForProvider :many
-- List models registered under a provider. Workspace-scoped.
SELECT
  m.public_id,
  m.name,
  m.display_name,
  m.context_window,
  m.max_output_tokens,
  m.input_price_micro_usd_per_mtok,
  m.output_price_micro_usd_per_mtok,
  m.supports_tools,
  m.supports_vision,
  m.updated_at,
  m.created_at,
  COUNT(*) OVER() AS total
FROM ai_models m
INNER JOIN ai_providers p ON p.id = m.provider_id AND p.enabled = TRUE
WHERE m.workspace_id = ?
  AND p.public_id = ?
  AND m.enabled = TRUE
ORDER BY m.sort_weight ASC, m.created_at DESC, m.public_id DESC
LIMIT ? OFFSET ?;
