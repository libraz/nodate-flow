-- v_workspace_members
-- Workspace membership joined with user display fields.
CREATE OR REPLACE VIEW v_workspace_members AS
SELECT
  wm.workspace_id,
  wm.public_id,
  u.public_id AS user_public_id,
  u.email,
  u.display_name,
  u.avatar_url,
  u.avatar_storage_object_id,
  so_avatar.public_id AS avatar_storage_object_public_id,
  wm.role,
  wm.invited_at,
  wm.joined_at,
  wm.updated_at,
  wm.created_at
FROM workspace_members wm
INNER JOIN users u
  ON u.id = wm.user_id AND u.enabled = TRUE
INNER JOIN workspaces w
  ON w.id = wm.workspace_id AND w.enabled = TRUE
LEFT JOIN storage_objects so_avatar
  ON so_avatar.id = u.avatar_storage_object_id AND so_avatar.enabled = TRUE
WHERE wm.enabled = TRUE;
