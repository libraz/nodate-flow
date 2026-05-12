-- name: AdminFindWorkspaceIdByPublicId :one
-- Resolve internal workspace_id from public_id for the admin purge driver.
-- Unlike GetWorkspaceIdByPublicId this does NOT filter by enabled, since
-- purge by design targets soft-disabled workspaces. sql.ErrNoRows means
-- the workspace is already gone and the caller maps it to a 404.
SELECT id
FROM workspaces
WHERE public_id = ?
LIMIT 1;

-- name: HardDeleteWorkspace :execresult
-- Final stage of the admin workspace immediate destructive delete. Fires
-- in the same request as the caller's MinIO sweep; there is no soft-
-- disabled prerequisite (the `enabled` flag is reserved for reversible
-- suspension and is unrelated to delete). RowsAffected = 0 means the
-- workspace was already removed by a concurrent request and the caller
-- should map it to a 404, NOT retry.
--
-- The ON DELETE CASCADE chain on workspaces.id removes every workspace-
-- scoped row (members, projects, tasks, events, attachments rows, and
-- the storage_objects rows whose blobs the caller already purged from
-- MinIO).
DELETE FROM workspaces
WHERE id = ?;

-- name: HardDeleteUser :execresult
-- Final stage of the admin user immediate destructive delete. Fires in
-- the same request as the caller's MinIO sweep (avatar storage_objects
-- + per-attachment ref_count decrements); there is no soft-disabled
-- prerequisite (the `enabled` flag is reserved for reversible suspension
-- and is unrelated to delete). RowsAffected = 0 means the user was
-- already removed by a concurrent request and the caller should map it
-- to a 404, NOT retry.
--
-- The ON DELETE CASCADE chain on users.id removes user-scoped rows
-- (sessions, recovery codes, instance admin grants, the user-scoped
-- storage_objects rows whose blobs the caller already purged). FK
-- ON DELETE SET NULL on attachments.uploader_id / similar audit-trail
-- back-refs is intentional: the audit history outlives the user.
DELETE FROM users
WHERE id = ?;
