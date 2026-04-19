-- name: LockTaskForTransition :one
-- Acquire a row-level lock on the task inside an open transaction so that
-- concurrent transition requests serialize correctly. Without FOR UPDATE two
-- requests can read the same derived_state, both validate the transition,
-- and both apply — producing an invalid state.
SELECT id, derived_state, project_id, workspace_id
FROM tasks
WHERE id = ? AND workspace_id = ? AND enabled = TRUE
FOR UPDATE;
