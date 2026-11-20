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

-- name: SatisfyConstraint :execrows
-- Mark a constraint as satisfied. The row matches only when the outcome
-- changes, so :execrows = 1 means "changed". An already satisfied row does not
-- match, which keeps the time it first became satisfied.
UPDATE task_constraints
SET satisfied_at = CURRENT_TIMESTAMP,
    failed_at = NULL
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
  AND satisfied_at IS NULL;

-- name: FailConstraint :execrows
-- Mark a constraint as currently failing; clears satisfied_at so a constraint
-- that stops holding no longer reads as satisfied. The row matches only when
-- the outcome changes, so :execrows = 1 means "changed". An already failing
-- row does not match, which keeps the time the failure began.
UPDATE task_constraints
SET failed_at = CURRENT_TIMESTAMP,
    satisfied_at = NULL
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
  AND failed_at IS NULL;

-- name: DeleteConstraint :execrows
-- Soft-delete a constraint. Scoped by the owning task_id so a sibling task's
-- constraint cannot be deleted through another task's path; the affected-row
-- count lets the handler return NOT_FOUND on a no-op.
UPDATE task_constraints
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?
  AND task_id = ?
  AND enabled = TRUE;
