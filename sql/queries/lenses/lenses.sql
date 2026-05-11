-- name: CreateLens :execlastid
-- Insert a new saved lens (view).
INSERT INTO lenses (
  public_id,
  workspace_id,
  project_id,
  creator_id,
  name,
  description,
  lens_json,
  is_default,
  sort_weight
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListLensesForProject :many
-- List enabled lenses scoped to a workspace + project (or workspace-wide when project_id IS NULL).
SELECT
  l.public_id,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  l.name,
  l.description,
  l.lens_json,
  l.is_default,
  l.is_public,
  l.public_token,
  l.shared_at,
  l.safety_checked_at,
  l.sort_weight,
  l.updated_at,
  l.created_at,
  COUNT(*) OVER() AS total
FROM lenses l
INNER JOIN users u ON u.id = l.creator_id AND u.enabled = TRUE
WHERE l.workspace_id = ?
  AND (l.project_id = ? OR l.project_id IS NULL)
  AND l.enabled = TRUE
ORDER BY l.is_default DESC, l.sort_weight ASC, l.created_at DESC
LIMIT ? OFFSET ?;

-- name: GetLensByPublicID :one
-- Fetch a single lens by its public_id.
SELECT
  l.public_id,
  l.workspace_id,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  l.name,
  l.description,
  l.lens_json,
  l.is_default,
  l.is_public,
  l.public_token,
  l.shared_at,
  l.safety_checked_at,
  l.sort_weight,
  l.updated_at,
  l.created_at
FROM lenses l
INNER JOIN users u ON u.id = l.creator_id AND u.enabled = TRUE
WHERE l.workspace_id = ?
  AND l.public_id = ?
  AND l.enabled = TRUE;

-- name: UpdateLens :exec
-- Update a lens name, description and/or JSON body.
UPDATE lenses
SET name = ?,
    description = ?,
    lens_json = ?,
    is_default = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: ResolveLensProjectID :one
-- Resolve a lens public_id to its optional internal project_id.
-- Used by the export handler to decide workspace-wide vs project-scoped queries.
SELECT
  l.project_id
FROM lenses l
WHERE l.workspace_id = ?
  AND l.public_id = ?
  AND l.enabled = TRUE;

-- name: DeleteLens :exec
-- Soft-delete a lens.
UPDATE lenses
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;
