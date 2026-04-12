-- name: FindLensByPublicToken :one
-- Look up a publicly shared lens by its token. Used by unauthenticated
-- public share endpoints. Only returns enabled + public lenses.
SELECT
  l.id,
  l.public_id,
  l.workspace_id,
  w.public_id AS workspace_public_id,
  l.project_id,
  l.name,
  l.lens_json,
  l.shared_at,
  l.safety_checked_at,
  l.created_at
FROM lenses l
INNER JOIN workspaces w ON w.id = l.workspace_id AND w.enabled = TRUE
WHERE l.public_token = ?
  AND l.is_public = TRUE
  AND l.enabled = TRUE
LIMIT 1;

-- name: SetLensPublic :exec
-- Enable public sharing on a lens. Generates a share URL token.
-- No-op if the lens is already public (WHERE is_public = FALSE guard).
UPDATE lenses
SET is_public = TRUE,
    public_token = ?,
    shared_at = NOW(3)
WHERE workspace_id = ?
  AND public_id = ?
  AND is_public = FALSE
  AND enabled = TRUE;

-- name: SetLensPrivate :exec
-- Revoke public sharing on a lens. Clears the token.
-- No-op if the lens is already private (WHERE is_public = TRUE guard).
UPDATE lenses
SET is_public = FALSE,
    public_token = NULL
WHERE workspace_id = ?
  AND public_id = ?
  AND is_public = TRUE
  AND enabled = TRUE;

-- name: UpdateLensSafetyCheck :exec
-- Record the timestamp of the latest AI safety check for a public lens.
UPDATE lenses
SET safety_checked_at = NOW(3)
WHERE id = ?;
