-- name: CreateMention :execlastid
-- Insert a mention record after extracting @mentions from markdown.
INSERT INTO mentions (
  public_id,
  workspace_id,
  mentioned_user_id,
  actor_user_id,
  task_id,
  comment_id,
  source
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListMentionsForUser :many
-- List mentions where a user was mentioned, within a workspace.
SELECT
  m.public_id,
  m.mentioned_user_id,
  mu.public_id AS mentioned_public_id,
  mu.display_name AS mentioned_display_name,
  m.actor_user_id,
  au.public_id AS actor_public_id,
  au.display_name AS actor_display_name,
  m.task_id,
  m.comment_id,
  m.source,
  m.created_at,
  COUNT(*) OVER() AS total
FROM mentions m
INNER JOIN users mu ON mu.id = m.mentioned_user_id
LEFT JOIN users au ON au.id = m.actor_user_id
WHERE m.workspace_id = ?
  AND m.mentioned_user_id = ?
  AND m.enabled = TRUE
ORDER BY m.created_at DESC
LIMIT ? OFFSET ?;

-- name: ListMentionsForTask :many
-- List all mentions within a specific task (description or comments).
SELECT
  m.public_id,
  m.mentioned_user_id,
  mu.public_id AS mentioned_public_id,
  mu.display_name AS mentioned_display_name,
  m.actor_user_id,
  au.public_id AS actor_public_id,
  m.source,
  m.created_at
FROM mentions m
INNER JOIN users mu ON mu.id = m.mentioned_user_id
LEFT JOIN users au ON au.id = m.actor_user_id
WHERE m.task_id = ?
  AND m.enabled = TRUE
ORDER BY m.created_at ASC;

-- name: DeleteMentionsForTaskDescription :exec
-- Remove all task_description mentions for a task (before re-extracting).
UPDATE mentions
SET enabled = FALSE
WHERE task_id = ?
  AND source = 'task_description';

-- name: DeleteMentionsForComment :exec
-- Remove all mentions for a specific comment (before re-extracting).
UPDATE mentions
SET enabled = FALSE
WHERE comment_id = ?;
