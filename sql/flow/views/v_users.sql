-- v_users
-- Workspace-scoped user projection. Joins workspace_members so the view
-- carries workspace_id for tenant guards. Internal ids are not selected.
CREATE OR REPLACE VIEW v_users AS
SELECT
  wm.workspace_id,
  u.public_id,
  u.email,
  u.display_name,
  u.avatar_url,
  u.avatar_storage_object_id,
  so_avatar.public_id AS avatar_storage_object_public_id,
  u.locale,
  u.timezone,
  u.country,
  u.week_start,
  u.theme_preference,
  u.calendar_shift_default,
  wm.role AS workspace_role,
  u.last_login_at,
  u.updated_at,
  u.created_at
FROM users u
INNER JOIN workspace_members wm
  ON wm.user_id = u.id AND wm.enabled = TRUE
INNER JOIN workspaces w
  ON w.id = wm.workspace_id AND w.enabled = TRUE
LEFT JOIN storage_objects so_avatar
  ON so_avatar.id = u.avatar_storage_object_id AND so_avatar.enabled = TRUE
WHERE u.enabled = TRUE;
