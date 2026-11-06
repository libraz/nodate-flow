-- v_comment_for_task
-- Shared projection used by ListCommentsForTask (offset variant) and
-- ListCommentsForTaskKeyset (keyset variant). Encapsulates the comments
-- + tasks + users JOINs so both query variants stay aligned on
-- visibility (`enabled = TRUE` propagation) and column shape.
--
-- The author is LEFT JOINed. A comment is part of the task's history and
-- belongs to the task, not to the account that typed it, so suspending
-- that account must not delete the discussion for everyone else on the
-- team. `enabled = TRUE` stays in the ON clause, so a suspended author's
-- identity columns come back NULL and the comment renders as written by an
-- unknown user. The rule this follows: `enabled = TRUE` may gate a row only
-- when the user is what the row is about (membership, credentials, actor
-- lists); when the user is merely the byline on someone else's content, it
-- gates the byline. An agent byline falls back to the name and public id
-- snapshotted on the comment once the agent row is deleted.
CREATE OR REPLACE ALGORITHM=MERGE VIEW v_comment_for_task AS
SELECT
  c.workspace_id,
  c.task_id,
  t.public_id AS task_public_id,
  c.public_id,
  c.author_kind,
  u.public_id AS author_public_id,
  CAST(COALESCE(BIN_TO_UUID(ag.public_id, 0), BIN_TO_UUID(c.author_agent_public_id, 0), '') AS CHAR(36)) AS author_agent_public_id,
  COALESCE(u.display_name, ag.name, c.author_agent_name, '') AS author_display_name,
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
LEFT JOIN users u ON u.id = c.author_id AND u.enabled = TRUE
-- No enabled filter: the agent is the historical byline, not what the row is about.
LEFT JOIN ai_agents ag ON ag.id = c.author_agent_id
LEFT JOIN storage_objects so_author_avatar
  ON so_author_avatar.id = u.avatar_storage_object_id AND so_author_avatar.enabled = TRUE
WHERE c.enabled = TRUE;
