-- name: AddConstraint :execlastid
-- Attach a new constraint to a task.
INSERT INTO task_constraints (
  public_id,
  workspace_id,
  task_id,
  kind,
  expression
) VALUES (?, ?, ?, ?, ?);

-- name: ListConstraintsForTask :many
-- List a task's constraints. The task is resolved by public_id outside.
SELECT
  tc.public_id,
  tc.kind,
  tc.expression,
  tc.satisfied_at,
  tc.failed_at,
  tc.sort_weight,
  tc.updated_at,
  tc.created_at,
  COUNT(*) OVER() AS total
FROM task_constraints tc
INNER JOIN tasks t ON t.id = tc.task_id AND t.enabled = TRUE
WHERE tc.workspace_id = ?
  AND t.public_id = ?
  AND tc.enabled = TRUE
ORDER BY tc.sort_weight ASC, tc.created_at ASC, tc.public_id ASC
LIMIT ? OFFSET ?;

-- name: SatisfyConstraint :exec
-- Mark a constraint as satisfied at the current time.
UPDATE task_constraints
SET satisfied_at = CURRENT_TIMESTAMP,
    failed_at = NULL
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: FailConstraint :exec
-- Mark a constraint as currently failing. Clears satisfied_at so the
-- transition is visible in v_task_constraint_satisfaction.
UPDATE task_constraints
SET failed_at = CURRENT_TIMESTAMP,
    satisfied_at = NULL
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: DeleteConstraint :exec
-- Soft-delete a constraint.
UPDATE task_constraints
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;
