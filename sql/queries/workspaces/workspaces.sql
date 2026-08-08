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
-- Fetch just the timezone and country columns by internal id. Used by the
-- calendar layer when resolving the effective timezone for a request.
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

-- name: CountEnabledWorkspaces :one
-- Number of live tenants on this instance. Used by the inbound webhook
-- receivers to decide whether NF_FLOW_DEFAULT_WORKSPACE_ID may act as a
-- fallback for an unmapped sender: that fallback is only meaningful on a
-- single-tenant deployment, and the moment a second workspace exists it
-- would start delivering one tenant's events to another.
SELECT COUNT(*) AS total
FROM workspaces
WHERE enabled = TRUE;

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

-- name: UpdateWorkspace :execrows
-- Update mutable workspace fields by public_id.
UPDATE workspaces
SET name = ?,
    description = ?,
    icon_url = ?
WHERE public_id = ?
  AND enabled = TRUE;

-- name: UpdateWorkspaceFull :execrows
-- Update workspace name and slug by public_id.
UPDATE workspaces
SET name = ?,
    slug = ?
WHERE public_id = ?
  AND enabled = TRUE;

-- name: PatchWorkspace :execrows
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
