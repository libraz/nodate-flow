-- name: UpsertViewPreference :exec
-- Create or update a user's view preference for a specific scope.
-- Conflicts on (workspace_id, user_id, scope_type, scope_key); scope_key is
-- generated from the scope_public_id supplied below, so every column of the
-- key is determined by this statement's own values.
--
-- enabled is reset because the reads below only return live rows: without it a
-- save against a soft-deleted row updates that row in place, reports success,
-- and leaves the preference still invisible.
INSERT INTO user_view_preferences (
  public_id,
  workspace_id,
  user_id,
  scope_type,
  scope_public_id,
  prefs_json
) VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  prefs_json = VALUES(prefs_json),
  enabled = TRUE,
  updated_at = NOW();

-- name: FindViewPreference :one
-- Get a user's view preference for a specific scope.
SELECT
  uvp.public_id,
  uvp.scope_type,
  uvp.scope_public_id,
  uvp.prefs_json,
  uvp.updated_at,
  uvp.created_at
FROM user_view_preferences uvp
WHERE uvp.workspace_id = ?
  AND uvp.user_id = ?
  AND uvp.scope_type = ?
  AND (uvp.scope_public_id = ? OR (uvp.scope_public_id IS NULL AND ? IS NULL))
  AND uvp.enabled = TRUE;

-- name: ListViewPreferencesForUser :many
-- List all view preferences for a user in a workspace.
SELECT
  uvp.public_id,
  uvp.scope_type,
  uvp.scope_public_id,
  uvp.prefs_json,
  uvp.updated_at,
  uvp.created_at
FROM user_view_preferences uvp
WHERE uvp.workspace_id = ?
  AND uvp.user_id = ?
  AND uvp.enabled = TRUE
ORDER BY uvp.scope_type ASC;
