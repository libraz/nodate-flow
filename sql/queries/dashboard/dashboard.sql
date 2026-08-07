-- name: CreateWidget :execlastid
-- Insert a new dashboard widget.
INSERT INTO dashboard_widgets (
  public_id,
  workspace_id,
  creator_id,
  widget_type,
  title,
  config,
  position_x,
  position_y,
  width,
  height
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListWidgetsForWorkspace :many
-- List enabled widgets for a workspace with creator info, ordered by sort_weight.
-- The creator is LEFT JOINed: a widget belongs to the workspace dashboard
-- everyone shares, so suspending the account that placed it must not blank
-- out the dashboard for the rest of the team. `enabled = TRUE` stays in the
-- ON clause so a suspended creator's identity is withheld rather than the
-- widget disappearing.
SELECT
  dw.public_id,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  dw.widget_type,
  dw.title,
  dw.config,
  dw.position_x,
  dw.position_y,
  dw.width,
  dw.height,
  dw.sort_weight,
  dw.updated_at,
  dw.created_at,
  COUNT(*) OVER() AS total
FROM dashboard_widgets dw
LEFT JOIN users u ON u.id = dw.creator_id AND u.enabled = TRUE
WHERE dw.workspace_id = ?
  AND dw.enabled = TRUE
ORDER BY dw.sort_weight ASC, dw.created_at ASC, dw.id ASC
LIMIT ? OFFSET ?;

-- name: GetWidgetByPublicID :one
-- Fetch a single widget by workspace_id + public_id.
SELECT
  dw.public_id,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  dw.widget_type,
  dw.title,
  dw.config,
  dw.position_x,
  dw.position_y,
  dw.width,
  dw.height,
  dw.sort_weight,
  dw.updated_at,
  dw.created_at
FROM dashboard_widgets dw
LEFT JOIN users u ON u.id = dw.creator_id AND u.enabled = TRUE
WHERE dw.workspace_id = ?
  AND dw.public_id = ?
  AND dw.enabled = TRUE
LIMIT 1;

-- name: UpdateWidget :exec
-- Update mutable widget fields. Uses sqlc.narg for optional partial updates.
UPDATE dashboard_widgets
SET title      = COALESCE(sqlc.narg('title'), title),
    config     = COALESCE(sqlc.narg('config'), config),
    position_x = COALESCE(sqlc.narg('position_x'), position_x),
    position_y = COALESCE(sqlc.narg('position_y'), position_y),
    width      = COALESCE(sqlc.narg('width'), width),
    height     = COALESCE(sqlc.narg('height'), height)
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: UpdateWidgetPosition :exec
-- Reposition a single widget on the grid (called N times for batch reorder).
UPDATE dashboard_widgets
SET position_x   = ?,
    position_y   = ?,
    width        = ?,
    height       = ?,
    sort_weight  = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: DisableWidget :exec
-- Soft-delete a widget.
UPDATE dashboard_widgets
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;
