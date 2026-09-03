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
LEFT JOIN (
  SELECT ta_inner.task_id, ta_inner.user_id,
         ROW_NUMBER() OVER (PARTITION BY ta_inner.task_id ORDER BY ta_inner.id ASC) AS rn
  FROM task_actors ta_inner
  WHERE ta_inner.role = 'assignee' AND ta_inner.enabled = TRUE AND ta_inner.kind = 'user'
) ta ON ta.task_id = t.id AND ta.rn = 1
LEFT JOIN users u ON u.id = ta.user_id AND u.enabled = TRUE
WHERE t.workspace_id = ?
  AND t.enabled = TRUE
  AND t.id IN (sqlc.slice('task_ids'))
  -- These titles go into the LLM prompt, so the set is the one the actor
  -- may read. Elevated roles skip the check.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR t.visibility = 'public'
    OR (t.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = t.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (t.visibility = 'private' AND (
      t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = t.id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  )
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
