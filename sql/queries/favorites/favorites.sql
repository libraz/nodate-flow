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
-- List all favorites for a user within a workspace, ordered by sort weight.
SELECT
  uf.public_id,
  uf.target_type,
  uf.target_public_id,
  uf.folder_name,
  uf.sort_weight,
  uf.created_at,
  COUNT(*) OVER() AS total
FROM user_favorites uf
WHERE uf.workspace_id = ?
  AND uf.user_id = ?
  AND uf.enabled = TRUE
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

-- name: DisableFavorite :exec
-- Soft-delete a favorite.
UPDATE user_favorites
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?
  AND user_id = ?;

-- name: FindFavoriteByTarget :one
-- Check if a user has already favorited this entity.
SELECT id, public_id
FROM user_favorites
WHERE workspace_id = ?
  AND user_id = ?
  AND target_type = ?
  AND target_public_id = ?
  AND enabled = TRUE;
