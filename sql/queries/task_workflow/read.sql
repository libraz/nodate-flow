-- Bounded task-workflow state for the task detail UI. The caller has passed
-- RequireTaskAccess, so every result remains scoped to the readable task.

-- name: ListRecentTaskWorkflowHandoffs :many
WITH recent AS (
  SELECT recent_handoff.id
  FROM task_handoffs AS recent_handoff
  WHERE recent_handoff.workspace_id = sqlc.arg(workspace_id)
    AND recent_handoff.task_id = sqlc.arg(task_id)
    AND recent_handoff.status <> 'pending'
  ORDER BY recent_handoff.id DESC
  LIMIT 20
)
SELECT
  BIN_TO_UUID(h.public_id) AS handoff_public_id,
  h.role,
  COALESCE(BIN_TO_UUID(from_user.public_id), '') AS from_user_public_id,
  COALESCE(BIN_TO_UUID(to_user.public_id), '') AS to_user_public_id,
  h.from_user_id,
  h.to_user_id,
  h.requested_by_user_id,
  h.status,
  h.due_at,
  h.created_at,
  h.resolved_at
FROM task_handoffs h
LEFT JOIN users from_user ON from_user.id = h.from_user_id
LEFT JOIN users to_user ON to_user.id = h.to_user_id
WHERE h.workspace_id = sqlc.arg(workspace_id)
  AND h.task_id = sqlc.arg(task_id)
  AND (h.status = 'pending' OR h.id IN (SELECT recent.id FROM recent))
ORDER BY h.id DESC
LIMIT 23;
