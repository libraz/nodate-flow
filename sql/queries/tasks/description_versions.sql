-- name: CreateDescriptionVersion :execlastid
-- Insert a new description version snapshot.
INSERT INTO task_description_versions (
  public_id, workspace_id, task_id, author_user_id, author_agent_id, author_agent_public_id, author_agent_name, version_number, body, body_length
) VALUES (?, ?, ?, ?, ?, NULLIF(sqlc.arg('author_agent_public_id'), 0x00000000000000000000000000000000), ?, ?, ?, ?);

-- name: ListDescriptionVersions :many
-- List all description versions for a task, newest first.
SELECT
  tdv.public_id,
  tdv.version_number,
  tdv.author_user_id,
  tdv.author_agent_id,
  tdv.author_agent_public_id,
  COALESCE(tdv.author_agent_name, aa.name, '') AS author_agent_name,
  au.public_id AS author_public_id,
  au.display_name AS author_display_name,
  tdv.body_length,
  tdv.created_at
FROM task_description_versions tdv
LEFT JOIN users au ON au.id = tdv.author_user_id
-- No enabled filter: the agent is the historical author of the version.
LEFT JOIN ai_agents aa ON aa.id = tdv.author_agent_id
WHERE tdv.workspace_id = ?
  AND tdv.task_id = ?
  AND tdv.enabled = TRUE
ORDER BY tdv.version_number DESC;

-- name: FindDescriptionVersion :one
-- Find a specific description version by public id.
SELECT
  tdv.public_id,
  tdv.version_number,
  tdv.author_user_id,
  tdv.author_agent_id,
  tdv.author_agent_public_id,
  COALESCE(tdv.author_agent_name, aa.name, '') AS author_agent_name,
  au.public_id AS author_public_id,
  au.display_name AS author_display_name,
  tdv.body,
  tdv.body_length,
  tdv.created_at
FROM task_description_versions tdv
LEFT JOIN users au ON au.id = tdv.author_user_id
-- No enabled filter: the agent is the historical author of the version.
LEFT JOIN ai_agents aa ON aa.id = tdv.author_agent_id
WHERE tdv.workspace_id = ?
  AND tdv.task_id = ?
  AND tdv.public_id = ?
  AND tdv.enabled = TRUE;

-- name: NextDescriptionVersionNumber :one
-- Get the next version number for a task's description history.
SELECT COALESCE(MAX(version_number), 0) + 1 AS next_version
FROM task_description_versions
WHERE task_id = ?
  AND enabled = TRUE;
