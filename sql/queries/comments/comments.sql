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
-- Reads from v_comment_for_task which centralizes the comments + tasks +
-- users JOINs and `enabled` propagation shared with the keyset variant.
SELECT
  v.public_id,
  v.author_public_id,
  v.author_display_name,
  v.author_avatar_url,
  v.body,
  v.edited_at,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_comment_for_task v
WHERE v.workspace_id = ?
  AND v.task_public_id = ?
ORDER BY v.created_at ASC, v.public_id ASC
LIMIT ? OFFSET ?;

-- name: ListCommentsForTaskKeyset :many
-- Keyset-paginated variant of ListCommentsForTask.
--
-- Cursor encoding: pass the (created_at, public_id) tuple from the LAST
-- row of the previous page. First page passes NULL for both
-- cursor_created_at and cursor_public_id. ORDER BY (created_at DESC,
-- public_id DESC) — note this reverses the OFFSET variant's ASC order
-- so the cursor semantics match the rest of the keyset family. UI
-- consumers that want oldest-first must reverse the page client-side.
--
-- Index used: idx_comments_task_id_keyset (task_id, created_at, public_id)
-- on the underlying comments table; the view is MERGEd by the optimizer.
SELECT
  v.public_id,
  v.author_public_id,
  v.author_display_name,
  v.author_avatar_url,
  v.body,
  v.edited_at,
  v.updated_at,
  v.created_at
FROM v_comment_for_task v
WHERE v.workspace_id = ?
  AND v.task_public_id = ?
  AND (sqlc.narg(cursor_created_at) IS NULL
       OR v.created_at < sqlc.narg(cursor_created_at)
       OR (v.created_at = sqlc.narg(cursor_created_at)
           AND v.public_id < sqlc.narg(cursor_public_id)))
ORDER BY v.created_at DESC, v.public_id DESC
LIMIT ?;

-- name: EditComment :execrows
-- Edit a comment body and stamp edited_at.
UPDATE comments
SET body = ?,
    edited_at = CURRENT_TIMESTAMP
WHERE workspace_id = ?
  AND public_id = ?
  AND author_id = ?
  AND enabled = TRUE;

-- name: DeleteComment :execrows
-- Soft-delete a comment.
UPDATE comments
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;
