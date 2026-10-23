-- name: CreateFavorite :execlastid
-- Add an entity to the user's favorites.
INSERT INTO user_favorites (
  public_id,
  workspace_id,
  user_id,
  target_type,
  target_public_id,
  folder_name
) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListFavoritesForUser :many
-- List a user's favorites within a workspace, ordered by sort weight.
-- Only favorites whose target still exists, is enabled, and is readable by
-- the caller are returned: a task through the Layer 4 visibility rule
-- (elevated roles skip it), a page only while every ancestor is enabled,
-- matching GetPageByPublicId. The favorite row itself is kept, so a target
-- that becomes readable again reappears.
WITH RECURSIVE enabled_pages (id) AS (
  SELECT pages.id FROM pages
  WHERE pages.workspace_id = sqlc.arg('workspace_id')
    AND pages.parent_page_id IS NULL
    AND pages.enabled = TRUE
  UNION ALL
  SELECT p.id FROM pages p
  INNER JOIN enabled_pages ep ON ep.id = p.parent_page_id
  WHERE p.workspace_id = sqlc.arg('workspace_id')
    AND p.enabled = TRUE
)
SELECT
  uf.public_id,
  uf.target_type,
  uf.target_public_id,
  uf.folder_name,
  uf.sort_weight,
  uf.created_at,
  COUNT(*) OVER() AS total
FROM user_favorites uf
WHERE uf.workspace_id = sqlc.arg('workspace_id')
  AND uf.user_id = ?
  AND uf.enabled = TRUE
  AND (
    (uf.target_type = 'project' AND EXISTS (
      SELECT 1 FROM projects pr
      WHERE pr.workspace_id = uf.workspace_id
        AND pr.public_id = uf.target_public_id
        AND pr.enabled = TRUE
    ))
    OR (uf.target_type = 'task' AND EXISTS (
      SELECT 1 FROM tasks t
      WHERE t.workspace_id = uf.workspace_id
        AND t.public_id = uf.target_public_id
        AND t.enabled = TRUE
        AND (
          CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
          OR t.visibility = 'public'
          OR (t.visibility = 'project' AND EXISTS (
            SELECT 1 FROM project_members pm_vis
            WHERE pm_vis.project_id = t.project_id
              AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
              AND pm_vis.enabled = TRUE
          ))
          OR (t.visibility = 'private' AND (
            t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            OR EXISTS (
              SELECT 1 FROM task_actors ta_vis
              WHERE ta_vis.task_id = t.id
                AND ta_vis.kind = 'user'
                AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
                AND ta_vis.enabled = TRUE
            )
          ))
        )
    ))
    OR (uf.target_type = 'page' AND EXISTS (
      SELECT 1 FROM pages pg
      WHERE pg.workspace_id = uf.workspace_id
        AND pg.public_id = uf.target_public_id
        AND pg.id IN (SELECT id FROM enabled_pages)
    ))
    OR (uf.target_type = 'lens' AND EXISTS (
      SELECT 1 FROM lenses l
      WHERE l.workspace_id = uf.workspace_id
        AND l.public_id = uf.target_public_id
        AND l.enabled = TRUE
    ))
    OR (uf.target_type = 'timebox' AND EXISTS (
      SELECT 1 FROM timeboxes tb
      WHERE tb.workspace_id = uf.workspace_id
        AND tb.public_id = uf.target_public_id
        AND tb.enabled = TRUE
    ))
  )
ORDER BY uf.sort_weight ASC, uf.created_at DESC
LIMIT ? OFFSET ?;

-- name: FindFavoriteByPublicId :one
-- Find a single favorite by public id.
SELECT
  uf.id,
  uf.public_id,
  uf.workspace_id,
  uf.user_id,
  uf.target_type,
  uf.target_public_id,
  uf.folder_name,
  uf.sort_weight,
  uf.created_at
FROM user_favorites uf
WHERE uf.workspace_id = ?
  AND uf.public_id = ?
  AND uf.user_id = ?
  AND uf.enabled = TRUE;

-- name: DisableFavorite :execrows
-- Soft-delete a favorite.
UPDATE user_favorites
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?
  AND user_id = ?
  AND enabled = TRUE;

-- name: FindFavoriteByTarget :one
-- Check if a user has already favorited this entity.
SELECT id, public_id
FROM user_favorites
WHERE workspace_id = ?
  AND user_id = ?
  AND target_type = ?
  AND target_public_id = ?
  AND enabled = TRUE;
