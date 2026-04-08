-- name: CreateAgent :execlastid
-- Insert a new reusable agent configuration.
INSERT INTO ai_agents (
  public_id,
  workspace_id,
  model_id,
  name,
  description,
  system_prompt,
  temperature,
  max_output_tokens,
  tools_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAgentsForWorkspace :many
-- List a workspace's agents joined with the underlying model.
SELECT
  a.public_id,
  m.public_id AS model_public_id,
  m.name      AS model_name,
  a.name,
  a.description,
  a.system_prompt,
  a.temperature,
  a.max_output_tokens,
  a.tools_json,
  a.updated_at,
  a.created_at,
  COUNT(*) OVER() AS total
FROM ai_agents a
INNER JOIN ai_models m ON m.id = a.model_id AND m.enabled = TRUE
WHERE a.workspace_id = ?
  AND a.enabled = TRUE
ORDER BY a.created_at DESC, a.public_id DESC
LIMIT ? OFFSET ?;
