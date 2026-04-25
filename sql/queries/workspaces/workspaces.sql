-- name: CreateWorkspace :execlastid
-- Insert a new workspace. Slug uniqueness is enforced at the DB level.
INSERT INTO workspaces (
  public_id,
  slug,
  name,
  description,
  icon_url,
  timezone,
  country
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: FindWorkspaceTimezoneCountryById :one
-- Fetch just the timezone and country columns by internal id. Used by
-- time-api when resolving the effective timezone for a request.
SELECT timezone, country
FROM workspaces
WHERE id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindWorkspaceByPublicId :one
-- Resolve a workspace by its UUID v7. Returns internal id for ACL.
SELECT
  id,
  public_id,
  slug,
  name,
  description,
  icon_url,
  timezone,
  country,
  enabled,
  updated_at,
  created_at
FROM workspaces
WHERE public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: GetWorkspaceIdByPublicId :one
-- Resolve internal workspace id from public_id; ignored if workspace is disabled.
-- Direct table lookup (no view) because this is a single-column id resolution
-- used on hot paths in task handlers; v_workspaces would project unused columns.
SELECT id
FROM workspaces
WHERE public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindWorkspaceBySlug :one
-- Resolve a workspace by slug. Returns internal id for ACL.
SELECT
  id,
  public_id,
  slug,
  name,
  description,
  icon_url,
  timezone,
  country,
  enabled,
  updated_at,
  created_at
FROM workspaces
WHERE slug = ?
  AND enabled = TRUE
LIMIT 1;

-- name: ListWorkspacesForUser :many
-- List workspaces a user belongs to.
SELECT
  w.public_id,
  w.slug,
  w.name,
  w.description,
  w.icon_url,
  w.timezone,
  w.country,
  wm.role,
  w.updated_at,
  w.created_at,
  (
    SELECT COUNT(*)
    FROM workspace_members wm2
    WHERE wm2.workspace_id = w.id
      AND wm2.enabled = TRUE
  ) AS member_count,
  COUNT(*) OVER() AS total
FROM workspace_members wm
INNER JOIN workspaces w
  ON w.id = wm.workspace_id AND w.enabled = TRUE
WHERE wm.user_id = ?
  AND wm.enabled = TRUE
ORDER BY w.created_at DESC, w.public_id DESC
LIMIT ? OFFSET ?;

-- name: UpdateWorkspace :exec
-- Update mutable workspace fields by public_id.
UPDATE workspaces
SET name = ?,
    description = ?,
    icon_url = ?
WHERE public_id = ?
  AND enabled = TRUE;

-- name: UpdateWorkspaceFull :exec
-- Update workspace name and slug by public_id.
UPDATE workspaces
SET name = ?,
    slug = ?
WHERE public_id = ?
  AND enabled = TRUE;

-- name: PatchWorkspace :exec
-- Patch a workspace via COALESCE; NULL params leave existing columns untouched.
UPDATE workspaces
SET name        = COALESCE(sqlc.narg('name'), name),
    slug        = COALESCE(sqlc.narg('slug'), slug),
    description = COALESCE(sqlc.narg('description'), description),
    icon_url    = COALESCE(sqlc.narg('icon_url'), icon_url),
    timezone    = COALESCE(sqlc.narg('timezone'), timezone),
    country     = COALESCE(sqlc.narg('country'), country)
WHERE public_id = ?
  AND enabled = TRUE;

-- name: DisableWorkspace :exec
-- Soft-disable a workspace. Cascade is handled by FK ON DELETE for hard purges.
UPDATE workspaces
SET enabled = FALSE
WHERE public_id = ?;
