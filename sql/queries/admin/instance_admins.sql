-- name: AdminListInstanceAdmins :many
-- List all instance admin grants with user details.
SELECT
  ia.public_id,
  u.public_id AS user_public_id,
  u.email,
  u.display_name,
  u.avatar_url,
  ia.granted_at,
  ia.revoked_at,
  ia.notes,
  ia.enabled,
  granter.public_id AS granted_by_public_id,
  granter.display_name AS granted_by_display_name,
  COUNT(*) OVER() AS total
FROM instance_admins ia
INNER JOIN users u ON u.id = ia.user_id
LEFT JOIN users granter ON granter.id = ia.granted_by_user_id
WHERE ia.enabled = TRUE AND ia.revoked_at IS NULL
ORDER BY ia.granted_at DESC
LIMIT ? OFFSET ?;

-- name: AdminGrantInstanceAdmin :execlastid
-- Grant instance admin to a user.
INSERT INTO instance_admins (public_id, user_id, granted_by_user_id, granted_at)
VALUES (?, ?, ?, NOW());

-- name: AdminBootstrapFirstInstanceAdmin :execrows
-- Atomically promote the calling user to the FIRST instance admin.
-- The INSERT...SELECT only materialises a row when no active admin yet
-- exists, so two concurrent /admin/setup calls can never both win: the
-- conditional and the write evaluate as one statement under a single
-- row lock, and the loser sees zero affected rows. Callers MUST inspect
-- RowsAffected and treat 0 as "already initialized".
INSERT INTO instance_admins (public_id, user_id, granted_by_user_id, granted_at)
SELECT ?, ?, ?, NOW()
FROM DUAL
WHERE NOT EXISTS (
  SELECT 1 FROM instance_admins WHERE enabled = TRUE AND revoked_at IS NULL
);

-- name: AdminRevokeInstanceAdmin :exec
-- Revoke an instance admin grant by setting revoked_at.
UPDATE instance_admins
SET revoked_at = NOW()
WHERE user_id = ? AND enabled = TRUE AND revoked_at IS NULL;

-- name: AdminCheckInstanceAdminExists :one
-- Check if any active instance admin exists (for bootstrap guard).
SELECT EXISTS(
  SELECT 1 FROM instance_admins
  WHERE enabled = TRUE AND revoked_at IS NULL
) AS has_admin;

-- name: AdminCountActiveInstanceAdmins :one
-- Count active instance admins (for last-admin guard).
SELECT COUNT(*) AS admin_count
FROM instance_admins
WHERE enabled = TRUE AND revoked_at IS NULL;

-- name: AdminFindInstanceAdminByUserId :one
-- Find instance admin grant for a specific user.
-- Used as an existence check by grant/revoke handlers (result is discarded).
SELECT public_id, user_id, granted_at, revoked_at
FROM instance_admins
WHERE user_id = ? AND enabled = TRUE AND revoked_at IS NULL
LIMIT 1;
