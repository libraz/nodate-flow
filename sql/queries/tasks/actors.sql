-- name: AddActor :execlastid
-- Attach a user to a task in the given role.
INSERT INTO task_actors (
  public_id,
  workspace_id,
  task_id,
  user_id,
  role
) VALUES (?, ?, ?, ?, ?);

-- name: ListActorsForTask :many
-- List actors on a task joined with user display fields.
SELECT
  ta.public_id,
  u.public_id AS user_public_id,
  u.email,
  u.display_name,
  u.avatar_url,
  ta.role,
  ta.updated_at,
  ta.created_at,
  COUNT(*) OVER() AS total
FROM task_actors ta
INNER JOIN tasks t ON t.id = ta.task_id AND t.enabled = TRUE
INNER JOIN users u ON u.id = ta.user_id AND u.enabled = TRUE
WHERE ta.workspace_id = ?
  AND t.public_id = ?
  AND ta.enabled = TRUE
ORDER BY ta.created_at ASC, ta.public_id ASC
LIMIT ? OFFSET ?;

-- name: AddAgentActor :execlastid
-- Attach an AI agent to a task in the given role.
INSERT INTO task_actors (
  public_id,
  workspace_id,
  task_id,
  agent_id,
  kind,
  role
) VALUES (?, ?, ?, ?, 'agent', ?);

-- name: ListAgentActorsForTask :many
-- List AI agent actors on a task joined with the agent definition.
SELECT
  ta.public_id,
  a.public_id AS agent_public_id,
  a.name      AS agent_name,
  ta.role,
  ta.updated_at,
  ta.created_at,
  COUNT(*) OVER() AS total
FROM task_actors ta
INNER JOIN tasks t     ON t.id = ta.task_id  AND t.enabled = TRUE
INNER JOIN ai_agents a ON a.id = ta.agent_id AND a.enabled = TRUE
WHERE ta.workspace_id = ?
  AND t.public_id = ?
  AND ta.kind = 'agent'
  AND ta.enabled = TRUE
ORDER BY ta.created_at ASC, ta.public_id ASC
LIMIT ? OFFSET ?;

-- name: FindAgentIDByPublicIDForWorkspace :one
-- Resolve an ai_agents public id to its internal id, scoped to the
-- workspace. Used by task actor handlers to bind by public id.
SELECT id
FROM ai_agents
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: RemoveActor :exec
-- Soft-remove an actor from a task.
UPDATE task_actors
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;
