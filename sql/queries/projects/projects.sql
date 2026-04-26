-- name: CreateProject :execlastid
-- Insert a new project in a workspace.
INSERT INTO projects (
  public_id,
  workspace_id,
  slug,
  identifier,
  name,
  description,
  color,
  started_on,
  ended_on
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindProjectByPublicId :one
-- Resolve a project by its UUID v7 within a workspace. Returns internal id.
SELECT
  id,
  public_id,
  workspace_id,
  slug,
  identifier,
  name,
  description,
  color,
  is_archived,
  started_on,
  ended_on,
  feature_pages,
  feature_timeboxes,
  feature_lenses,
  feature_calendar,
  enabled,
  updated_at,
  created_at
FROM projects
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: ListProjectsForWorkspace :many
-- List projects in a workspace via v_projects.
SELECT
  v.public_id,
  v.slug,
  v.identifier,
  v.name,
  v.description,
  v.color,
  v.is_archived,
  v.started_on,
  v.ended_on,
  v.sort_weight,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_projects v
WHERE v.workspace_id = ?
ORDER BY v.sort_weight ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: UpdateProject :exec
-- Update mutable project fields by public_id.
UPDATE projects
SET name = ?,
    identifier = ?,
    description = ?,
    color = ?,
    is_archived = ?,
    started_on = ?,
    ended_on = ?,
    feature_pages = ?,
    feature_timeboxes = ?,
    feature_lenses = ?,
    feature_calendar = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: FindProjectByPublicIdGlobal :one
-- Resolve a project by its UUID v7 without workspace scope.
-- Used by routes where workspace id is not part of the URL; ACL must be enforced separately.
SELECT
  p.id,
  p.public_id,
  p.workspace_id,
  w.public_id AS workspace_public_id,
  p.slug,
  p.identifier,
  p.name,
  p.description,
  p.color,
  p.is_archived,
  p.started_on,
  p.ended_on,
  p.feature_pages,
  p.feature_timeboxes,
  p.feature_lenses,
  p.feature_calendar,
  p.enabled,
  p.updated_at,
  p.created_at
FROM projects p
JOIN workspaces w ON w.id = p.workspace_id
WHERE p.public_id = ?
  AND p.enabled = TRUE
LIMIT 1;

-- name: UpdateProjectFull :exec
-- Update project name, slug and description by public_id.
UPDATE projects
SET name = ?,
    slug = ?,
    identifier = ?,
    description = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: DisableProjectChildTasks :exec
-- Cascade-disable every currently-enabled task that lives under a project.
-- Must be called inside the same transaction as DisableProject so callers
-- observe a consistent snapshot. The views (v_task_list, v_task_detail)
-- already AND-filter projects.enabled, but bypassing the view (raw SELECT
-- on tasks, MCP tooling, replay jobs) would otherwise still see live rows
-- under a disabled project. updated_at is bumped explicitly so audit
-- consumers can see the cascade event even though the column has an
-- ON UPDATE clause already.
-- workspace_id is included as the first WHERE clause for defense-in-depth
-- per project DB conventions, even though project_id is globally unique.
UPDATE tasks
SET enabled = FALSE,
    updated_at = CURRENT_TIMESTAMP(3)
WHERE workspace_id = ?
  AND project_id = ?
  AND enabled = TRUE;

-- name: DisableProject :exec
-- Soft-disable a project. The handler must call DisableProjectChildTasks
-- inside the same transaction to cascade enabled = FALSE down to tasks;
-- this query alone leaves child tasks live on the underlying table.
UPDATE projects
SET enabled = FALSE,
    updated_at = CURRENT_TIMESTAMP(3)
WHERE workspace_id = ?
  AND public_id = ?;

-- name: FindProjectByIdentifier :one
-- Resolve a project by its human-readable identifier within a workspace.
SELECT
  id,
  public_id,
  workspace_id,
  identifier,
  name
FROM projects
WHERE workspace_id = ?
  AND identifier = ?
  AND enabled = TRUE
LIMIT 1;
