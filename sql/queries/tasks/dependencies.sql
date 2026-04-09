-- name: AddDependency :execlastid
-- Add a directed dependency between two tasks.
INSERT INTO task_dependencies (
  public_id,
  workspace_id,
  from_task_id,
  to_task_id,
  kind
) VALUES (?, ?, ?, ?, ?);

-- name: ListDependenciesForTask :many
-- List outgoing dependencies of a task. Returns the target task public_id.
SELECT
  td.public_id,
  td.kind,
  ft.public_id AS from_task_public_id,
  tt.public_id AS to_task_public_id,
  tt.title    AS to_task_title,
  tt.derived_state AS to_task_derived_state,
  td.updated_at,
  td.created_at,
  COUNT(*) OVER() AS total
FROM task_dependencies td
INNER JOIN tasks ft ON ft.id = td.from_task_id AND ft.enabled = TRUE
INNER JOIN tasks tt ON tt.id = td.to_task_id   AND tt.enabled = TRUE
WHERE td.workspace_id = ?
  AND ft.public_id = ?
  AND td.enabled = TRUE
ORDER BY td.created_at ASC, td.public_id ASC
LIMIT ? OFFSET ?;

-- name: ListIncomingDependenciesForTask :many
-- List incoming dependencies of a task (edges that point AT this task).
SELECT
  td.public_id,
  td.kind,
  ft.public_id AS from_task_public_id,
  ft.title    AS from_task_title,
  ft.derived_state AS from_task_derived_state,
  tt.public_id AS to_task_public_id,
  td.updated_at,
  td.created_at,
  COUNT(*) OVER() AS total
FROM task_dependencies td
INNER JOIN tasks ft ON ft.id = td.from_task_id AND ft.enabled = TRUE
INNER JOIN tasks tt ON tt.id = td.to_task_id   AND tt.enabled = TRUE
WHERE td.workspace_id = ?
  AND tt.public_id = ?
  AND td.enabled = TRUE
ORDER BY td.created_at ASC, td.public_id ASC
LIMIT ? OFFSET ?;

-- name: ListDependenciesForProject :many
-- List every task_dependencies edge where BOTH endpoints belong to the
-- given project. Used by the Gantt chart (to draw dependency arrows)
-- and by the List / Board views (to compute "blocked by open" badges).
SELECT
  td.public_id,
  td.kind,
  ft.public_id AS from_task_public_id,
  ft.derived_state AS from_task_derived_state,
  tt.public_id AS to_task_public_id,
  tt.derived_state AS to_task_derived_state
FROM task_dependencies td
INNER JOIN tasks ft ON ft.id = td.from_task_id AND ft.enabled = TRUE
INNER JOIN tasks tt ON tt.id = td.to_task_id   AND tt.enabled = TRUE
INNER JOIN projects p ON p.id = ft.project_id AND p.enabled = TRUE
WHERE td.workspace_id = ?
  AND p.public_id = ?
  AND ft.project_id = tt.project_id
  AND td.enabled = TRUE
ORDER BY td.created_at ASC, td.public_id ASC;

-- name: DeleteDependency :exec
-- Soft-delete a dependency edge.
UPDATE task_dependencies
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;
