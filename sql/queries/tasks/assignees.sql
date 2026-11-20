-- name: ListTaskAssigneesForTasks :many
-- Human assignees for a page of tasks, in display order. Humans only,
-- matching assignee_count in _v_task_list_all: the INNER JOIN on users drops
-- agent actors (user_id IS NULL) and disabled users. The caller passes the
-- public ids of the page it is already returning, so the slice is bounded
-- by the page size.
SELECT
  t.public_id    AS task_public_id,
  u.public_id    AS user_public_id,
  u.display_name AS display_name
FROM tasks t
INNER JOIN task_actors ta
  ON ta.task_id = t.id
 AND ta.workspace_id = t.workspace_id
 AND ta.enabled = TRUE
 AND ta.role = 'assignee'
INNER JOIN users u
  ON u.id = ta.user_id
 AND u.enabled = TRUE
WHERE t.workspace_id = sqlc.arg('workspace_id')
  AND t.enabled = TRUE
  AND t.public_id IN (sqlc.slice('task_public_ids'))
ORDER BY t.id ASC, ta.sort_weight ASC, ta.id ASC;
