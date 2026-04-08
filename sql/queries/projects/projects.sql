-- name: CreateProject :execlastid
-- Insert a new project in a workspace.
INSERT INTO projects (
  public_id,
  workspace_id,
  slug,
  name,
  description,
  color,
  started_on,
  ended_on
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindProjectByPublicId :one
-- Resolve a project by its UUID v7 within a workspace. Returns internal id.
SELECT
  id,
  public_id,
  workspace_id,
  slug,
  name,
  description,
  color,
  is_archived,
  started_on,
  ended_on,
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
    description = ?,
    color = ?,
    is_archived = ?,
    started_on = ?,
    ended_on = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: FindProjectByPublicIdGlobal :one
-- Resolve a project by its UUID v7 without workspace scope.
-- Used by routes where workspace id is not part of the URL; ACL must be enforced separately.
SELECT
  id,
  public_id,
  workspace_id,
  slug,
  name,
  description,
  color,
  is_archived,
  started_on,
  ended_on,
  enabled,
  updated_at,
  created_at
FROM projects
WHERE public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: UpdateProjectFull :exec
-- Update project name, slug and description by public_id.
UPDATE projects
SET name = ?,
    slug = ?,
    description = ?
WHERE public_id = ?
  AND enabled = TRUE;

-- name: DisableProject :exec
-- Soft-disable a project.
UPDATE projects
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;
