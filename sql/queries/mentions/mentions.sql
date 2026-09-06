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
-- List mentions within a specific task (description or comments).
-- Paginated (LIMIT/OFFSET) so the result set is always bounded; total
-- carries the pre-page count.
SELECT
  m.public_id,
  m.mentioned_user_id,
  mu.public_id AS mentioned_public_id,
  mu.display_name AS mentioned_display_name,
  m.actor_user_id,
  au.public_id AS actor_public_id,
  m.source,
  m.created_at,
  COUNT(*) OVER() AS total
FROM mentions m
INNER JOIN users mu ON mu.id = m.mentioned_user_id
LEFT JOIN users au ON au.id = m.actor_user_id
WHERE m.task_id = ?
  AND m.workspace_id = ?
  AND m.enabled = TRUE
ORDER BY m.created_at ASC, m.public_id ASC
LIMIT ? OFFSET ?;

-- name: DeleteMentionsForTaskDescription :exec
-- Remove the workspace's task_description mentions for a task (before
-- re-extracting). A row here only caches what the description currently
-- says and is rebuilt from the body on every edit, so it is disposable:
-- the record that a mention happened lives in the event log. The
-- workspace bounds the clear: a task id alone would let one tenant's
-- re-extraction remove rows belonging to another. The source filter keeps
-- the task's comment mentions, which no description edit re-extracts.
--
-- affected-rows: not-applicable — it empties the description's cached
-- mentions so the new body's can be written, and a description that named
-- nobody carries none, which is already the state this is asked to
-- produce. No mention is being addressed here: the caller is rebuilding
-- the set, and the task it names is resolved before any of this runs.
DELETE FROM mentions
WHERE task_id = ?
  AND workspace_id = ?
  AND source = 'task_description';

-- name: DeleteMentionsForComment :exec
-- Remove the workspace's mentions for a specific comment (before
-- re-extracting). Disposable and workspace-bounded, as above.
--
-- affected-rows: not-applicable — same rebuild as
-- DeleteMentionsForTaskDescription and the same reason: a comment whose
-- previous body named nobody has nothing cached, and the comment itself is
-- resolved before the extraction that follows this.
DELETE FROM mentions
WHERE comment_id = ?
  AND workspace_id = ?;
