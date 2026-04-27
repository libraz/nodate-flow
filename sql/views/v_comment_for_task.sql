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
  c.body,
  c.edited_at,
  c.updated_at,
  c.created_at
FROM comments c
INNER JOIN tasks t ON t.id = c.task_id AND t.enabled = TRUE
INNER JOIN users u ON u.id = c.author_id AND u.enabled = TRUE
WHERE c.enabled = TRUE;
