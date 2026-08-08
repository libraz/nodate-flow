-- name: AdminListUsers :many
-- Paginated user list for instance admin panel.
-- search: pass '' to skip, otherwise matches email or display_name.
-- filter_enabled: pass NULL to skip, otherwise filters by enabled flag.
SELECT
  v.id,
  v.public_id,
  v.email,
  v.display_name,
  v.avatar_url,
  v.locale,
  v.timezone,
  v.country,
  v.last_login_at,
  v.email_verified_at,
  v.enabled,
  v.created_at,
  v.updated_at,
  v.workspace_count,
  v.is_instance_admin,
  COUNT(*) OVER() AS total
FROM v_admin_users v
WHERE (? = '' OR v.email LIKE CONCAT('%', ?, '%') OR v.display_name LIKE CONCAT('%', ?, '%'))
  AND (? IS NULL OR v.enabled = ?)
ORDER BY v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: AdminGetUser :one
-- Find a single user by public_id for admin detail view.
SELECT
  v.id,
  v.public_id,
  v.email,
  v.display_name,
  v.avatar_url,
  v.locale,
  v.timezone,
  v.country,
  v.last_login_at,
  v.email_verified_at,
  v.enabled,
  v.created_at,
  v.updated_at,
  v.workspace_count,
  v.is_instance_admin
FROM v_admin_users v
WHERE v.public_id = ?;

-- name: AdminSuspendUser :execrows
-- Disable a user account (soft-delete).
UPDATE users SET enabled = FALSE WHERE public_id = ? AND enabled = TRUE;

-- name: AdminEnableUser :execrows
-- Re-enable a previously suspended user account.
UPDATE users SET enabled = TRUE WHERE public_id = ? AND enabled = FALSE;

-- name: AdminIsInstanceAdmin :one
-- Check if a user_id has an active instance admin grant. Used by GET /me.
SELECT EXISTS(
  SELECT 1 FROM instance_admins
  WHERE user_id = ? AND enabled = TRUE AND revoked_at IS NULL
) AS is_admin;
