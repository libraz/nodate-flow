-- name: AddComment :execlastid
-- Append a comment to a task.
INSERT INTO comments (
  public_id,
  workspace_id,
  task_id,
  author_id,
  body
) VALUES (?, ?, ?, ?, ?);

-- name: ListCommentsForTask :many
-- List comments on a task joined with author display fields.
SELECT
  c.public_id,
  u.public_id AS author_public_id,
  u.display_name AS author_display_name,
  u.avatar_url AS author_avatar_url,
  c.body,
  c.edited_at,
  c.updated_at,
  c.created_at,
  COUNT(*) OVER() AS total
FROM comments c
INNER JOIN tasks t ON t.id = c.task_id AND t.enabled = TRUE
INNER JOIN users u ON u.id = c.author_id AND u.enabled = TRUE
WHERE c.workspace_id = ?
  AND t.public_id = ?
  AND c.enabled = TRUE
ORDER BY c.created_at ASC, c.public_id ASC
LIMIT ? OFFSET ?;

-- name: EditComment :exec
-- Edit a comment body and stamp edited_at.
UPDATE comments
SET body = ?,
    edited_at = CURRENT_TIMESTAMP
WHERE workspace_id = ?
  AND public_id = ?
  AND author_id = ?
  AND enabled = TRUE;

-- name: DeleteComment :exec
-- Soft-delete a comment.
UPDATE comments
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;
