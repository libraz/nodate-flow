-- v_comment_for_task
-- Shared projection used by ListCommentsForTask (offset variant) and
-- ListCommentsForTaskKeyset (keyset variant). Encapsulates the comments
-- + tasks + users JOINs so both query variants stay aligned on
-- visibility (`enabled = TRUE` propagation) and column shape.
CREATE OR REPLACE ALGORITHM=MERGE VIEW v_comment_for_task AS
SELECT
  c.workspace_id,
  c.task_id,
  t.public_id AS task_public_id,
  c.public_id,
  u.public_id AS author_public_id,
  u.display_name AS author_display_name,
  u.avatar_url AS author_avatar_url,
  u.avatar_storage_object_id AS author_avatar_storage_object_id,
  so_author_avatar.public_id AS author_avatar_storage_object_public_id,
  c.body,
  c.edited_at,
  c.updated_at,
  c.created_at
FROM comments c
INNER JOIN workspaces w ON w.id = c.workspace_id AND w.enabled = TRUE
INNER JOIN tasks t ON t.id = c.task_id AND t.enabled = TRUE
INNER JOIN users u ON u.id = c.author_id AND u.enabled = TRUE
LEFT JOIN storage_objects so_author_avatar
  ON so_author_avatar.id = u.avatar_storage_object_id AND so_author_avatar.enabled = TRUE
WHERE c.enabled = TRUE;
