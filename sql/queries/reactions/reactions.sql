-- name: CreateReaction :execlastid
-- Add an emoji reaction. Either task_id or comment_id must be set (not both).
INSERT INTO reactions (
  public_id,
  workspace_id,
  user_id,
  task_id,
  comment_id,
  emoji
) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListReactionsForTask :many
-- List reactions on a task with user info. Paginated (LIMIT/OFFSET) so
-- the result set is always bounded; total carries the pre-page count.
SELECT
  r.public_id,
  r.emoji,
  r.user_id,
  u.public_id AS user_public_id,
  u.display_name,
  r.created_at,
  COUNT(*) OVER() AS total
FROM reactions r
INNER JOIN users u ON u.id = r.user_id
WHERE r.task_id = ?
  AND r.workspace_id = ?
  AND r.enabled = TRUE
ORDER BY r.created_at ASC, r.public_id ASC
LIMIT ? OFFSET ?;

-- name: ListReactionsForComment :many
-- List reactions on a comment. Paginated (LIMIT/OFFSET) so the result
-- set is always bounded; total carries the pre-page count.
SELECT
  r.public_id,
  r.emoji,
  r.user_id,
  u.public_id AS user_public_id,
  u.display_name,
  r.created_at,
  COUNT(*) OVER() AS total
FROM reactions r
INNER JOIN users u ON u.id = r.user_id
WHERE r.comment_id = ?
  AND r.workspace_id = ?
  AND r.enabled = TRUE
ORDER BY r.created_at ASC, r.public_id ASC
LIMIT ? OFFSET ?;

-- name: DisableReaction :exec
-- Soft-delete a reaction by public id and user.
UPDATE reactions
SET enabled = FALSE
WHERE public_id = ?
  AND user_id = ?;

-- name: FindReactionByPublicId :one
-- Find a single reaction by public id.
SELECT
  r.id,
  r.public_id,
  r.workspace_id,
  r.user_id,
  r.task_id,
  r.comment_id,
  r.emoji,
  r.created_at
FROM reactions r
WHERE r.public_id = ?
  AND r.enabled = TRUE;

-- name: FindExistingReaction :one
-- Check if a user already reacted with a specific emoji on a task or comment.
SELECT id, public_id
FROM reactions
WHERE user_id = ?
  AND task_id = ?
  AND emoji = ?
  AND enabled = TRUE;
