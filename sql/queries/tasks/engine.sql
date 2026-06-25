-- Queries dedicated to the constraint engine.
-- Keyed off the internal task_id, but every read also filters on
-- workspace_id so the SQL boundary enforces tenant isolation even if
-- an upstream ACL check is bypassed. The engine never has to know
-- about public_id resolution.

-- name: GetTaskDueOnForEngine :one
SELECT due_on
FROM tasks
WHERE id = ? AND workspace_id = ? AND enabled = TRUE
LIMIT 1;

-- name: ListDependencyStatesForEngine :many
-- Outgoing dependencies of a task with the referenced task's
-- public_id, current derived_state, and dependency kind. The engine
-- builds a map[public_id]state and a map[public_id]kind from this
-- rowset; dependency.open_at_most uses the kind to ignore
-- non-blocking (informational) links.
SELECT
  tt.public_id   AS to_public_id,
  tt.derived_state,
  td.kind        AS dependency_kind
FROM task_dependencies td
INNER JOIN tasks tt ON tt.id = td.to_task_id AND tt.enabled = TRUE
WHERE td.from_task_id = ?
  AND td.workspace_id = ?
  AND td.enabled = TRUE;

-- name: ListTaskActorRolesForEngine :many
-- Distinct role names currently attached to the task via
-- task_actors. Used to populate Facts.ActorRoles.
SELECT DISTINCT role
FROM task_actors
WHERE task_id = ? AND workspace_id = ? AND enabled = TRUE;

-- name: ListTaskConstraintsForEngine :many
-- Enabled constraint rows for a task in evaluation order.
SELECT
  public_id,
  expression
FROM task_constraints
WHERE task_id = ? AND workspace_id = ? AND enabled = TRUE
ORDER BY sort_weight ASC, id ASC;
