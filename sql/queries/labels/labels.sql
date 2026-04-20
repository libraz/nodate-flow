-- name: CreateLabel :execlastid
-- Insert a new label in a workspace.
INSERT INTO labels (
  public_id,
  workspace_id,
  project_id,
  parent_label_id,
  name,
  color,
  description,
  sort_weight
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindLabelByPublicId :one
-- Resolve a label by its UUID v7 within a workspace.
SELECT
  id,
  public_id,
  workspace_id,
  project_id,
  parent_label_id,
  name,
  color,
  description,
  sort_weight,
  enabled,
  updated_at,
  created_at
FROM labels
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: ListLabelsForWorkspace :many
-- List all labels in a workspace, optionally filtered by project.
SELECT
  public_id,
  project_id,
  parent_label_id,
  name,
  color,
  description,
  sort_weight,
  updated_at,
  created_at,
  COUNT(*) OVER() AS total
FROM labels
WHERE workspace_id = ?
  AND enabled = TRUE
ORDER BY sort_weight ASC, name ASC
LIMIT ? OFFSET ?;

-- name: ListLabelsForProject :many
-- List labels scoped to a specific project (includes workspace-wide labels).
SELECT
  public_id,
  project_id,
  parent_label_id,
  name,
  color,
  description,
  sort_weight,
  updated_at,
  created_at,
  COUNT(*) OVER() AS total
FROM labels
WHERE workspace_id = ?
  AND (project_id = ? OR project_id IS NULL)
  AND enabled = TRUE
ORDER BY sort_weight ASC, name ASC
LIMIT ? OFFSET ?;

-- name: UpdateLabel :exec
-- Update mutable label fields.
UPDATE labels
SET name = ?,
    color = ?,
    description = ?,
    parent_label_id = ?,
    sort_weight = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: DisableLabel :exec
-- Soft-disable a label.
UPDATE labels
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;

-- name: CreateTaskLabel :execlastid
-- Attach a label to a task.
INSERT INTO task_labels (
  public_id,
  workspace_id,
  task_id,
  label_id,
  sort_weight
) VALUES (?, ?, ?, ?, ?);

-- name: DeleteTaskLabel :exec
-- Remove a label from a task (hard delete from junction).
DELETE FROM task_labels
WHERE workspace_id = ?
  AND task_id = ?
  AND label_id = ?;

-- name: ListTaskLabels :many
-- List labels attached to a task.
SELECT
  l.public_id,
  l.name,
  l.color,
  l.description,
  tl.sort_weight,
  tl.created_at
FROM task_labels tl
INNER JOIN labels l ON l.id = tl.label_id AND l.enabled = TRUE
WHERE tl.workspace_id = ?
  AND tl.task_id = ?
  AND tl.enabled = TRUE
ORDER BY tl.sort_weight ASC, tl.id ASC;

-- name: FindTaskLabelByIds :one
-- Check if a specific task-label junction exists.
SELECT
  tl.id,
  tl.public_id
FROM task_labels tl
WHERE tl.workspace_id = ?
  AND tl.task_id = ?
  AND tl.label_id = ?
  AND tl.enabled = TRUE
LIMIT 1;

-- name: DisableTaskLabel :exec
-- Soft-disable a task-label junction.
UPDATE task_labels
SET enabled = FALSE
WHERE workspace_id = ?
  AND task_id = ?
  AND label_id = ?;

-- name: FindLabelByWorkspaceAndName :one
-- Find a label by name within a workspace (for MCP resolve).
SELECT
  id,
  public_id,
  name,
  color
FROM labels
WHERE workspace_id = ?
  AND name = ?
  AND enabled = TRUE
LIMIT 1;
