-- ============================================================================
-- smart-create queries
-- Support LLM-powered task creation: retrieve similar tasks with their
-- assignees so the AI can infer who should handle new work.
-- ============================================================================

-- name: ListTasksWithAssigneesForSmartCreate :many
-- Fetch tasks and their primary assignees for a set of task IDs.
-- Used after cosine-ranking candidate embeddings in Go to enrich the
-- top-N similar tasks with assignee information for the LLM prompt.
SELECT
  t.id,
  t.public_id,
  t.title,
  ta.user_id AS assignee_user_id,
  u.display_name AS assignee_name,
  u.public_id AS assignee_public_id
FROM tasks t
LEFT JOIN task_actors ta ON ta.task_id = t.id
  AND ta.role = 'assignee' AND ta.enabled = TRUE AND ta.kind = 'user'
LEFT JOIN users u ON u.id = ta.user_id AND u.enabled = TRUE
WHERE t.workspace_id = ?
  AND t.enabled = TRUE
  AND t.id IN (sqlc.slice('task_ids'))
ORDER BY t.id ASC
LIMIT 200;

-- name: ListWorkspaceMembersForSmartCreate :many
-- List workspace members with display names for the LLM assignee prompt.
-- Uses v_workspace_members view. Returns all enabled members so the LLM
-- can suggest from the full pool.
SELECT
  v.user_public_id,
  v.display_name,
  v.email
FROM v_workspace_members v
WHERE v.workspace_id = ?
ORDER BY v.display_name ASC, v.user_public_id ASC
LIMIT 200;
