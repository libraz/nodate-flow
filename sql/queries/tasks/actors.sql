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

-- name: RemoveActor :exec
-- Soft-remove an actor from a task.
UPDATE task_actors
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;
