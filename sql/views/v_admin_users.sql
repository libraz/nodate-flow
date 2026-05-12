-- v_admin_users
-- Instance-scoped user view for admin panel. Unlike v_users, this is NOT
-- workspace-scoped and includes all users regardless of membership.
CREATE OR REPLACE VIEW v_admin_users AS
SELECT
  u.id,
  u.public_id,
  u.email,
  u.display_name,
  u.avatar_url,
  u.avatar_storage_object_id,
  so_avatar.public_id AS avatar_storage_object_public_id,
  u.locale,
  u.timezone,
  u.country,
  u.last_login_at,
  u.email_verified_at,
  u.enabled,
  u.created_at,
  u.updated_at,
  (SELECT COUNT(*) FROM workspace_members wm
     WHERE wm.user_id = u.id AND wm.enabled = TRUE) AS workspace_count,
  EXISTS(SELECT 1 FROM instance_admins ia
     WHERE ia.user_id = u.id AND ia.enabled = TRUE
       AND ia.revoked_at IS NULL) AS is_instance_admin
FROM users u
LEFT JOIN storage_objects so_avatar
  ON so_avatar.id = u.avatar_storage_object_id AND so_avatar.enabled = TRUE;
