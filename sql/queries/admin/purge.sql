-- name: AdminFindWorkspaceIdByPublicId :one
-- Resolve internal workspace_id from public_id for the admin purge driver.
-- Unlike GetWorkspaceIdByPublicId this does NOT filter by enabled, since
-- purge by design targets soft-disabled workspaces. sql.ErrNoRows means
-- the workspace is already gone and the caller maps it to a 404.
SELECT id
FROM workspaces
WHERE public_id = ?
LIMIT 1;

-- name: DeleteAttachmentsByWorkspace :exec
-- Clear the workspace's task attachments ahead of HardDeleteWorkspace.
--
-- attachments.storage_object_id is ON DELETE RESTRICT, while both
-- attachments.workspace_id and storage_objects.workspace_id are ON DELETE
-- CASCADE. Deleting the workspace therefore only succeeds if InnoDB happens
-- to reach `attachments` before `storage_objects` while walking the cascade
-- chain — an ordering that follows table creation order and is not part of
-- any documented contract. Removing the referrers up front makes the
-- teardown independent of it.
--
-- affected-rows: not-applicable — this clears whatever the workspace holds
-- ahead of the delete that answers the caller. HardDeleteWorkspace reports
-- whether the workspace was there; a workspace with no attachments is the
-- ordinary case and produces exactly the state this asks for.
DELETE FROM attachments
WHERE workspace_id = ?;

-- name: DeleteCalendarEventAttachmentsByWorkspace :exec
-- Calendar-side counterpart of DeleteAttachmentsByWorkspace;
-- calendar_event_attachments.storage_object_id carries the same
-- ON DELETE RESTRICT. See that query for the full rationale.
--
-- affected-rows: not-applicable — same shape and same reason as
-- DeleteAttachmentsByWorkspace: it clears a set ahead of the delete that
-- answers the caller, and an empty set is the ordinary case.
DELETE FROM calendar_event_attachments
WHERE workspace_id = ?;

-- name: HardDeleteWorkspace :execresult
-- Final stage of the admin workspace immediate destructive delete. Fires
-- in the same request as the caller's MinIO sweep; there is no soft-
-- disabled prerequisite (the `enabled` flag is reserved for reversible
-- suspension and is unrelated to delete). RowsAffected = 0 means the
-- workspace was already removed by a concurrent request and the caller
-- should map it to a 404, NOT retry.
--
-- The ON DELETE CASCADE chain on workspaces.id removes every workspace-
-- scoped row (members, projects, tasks, events, and the storage_objects
-- rows whose blobs the caller already purged from MinIO). Callers MUST
-- have run DeleteAttachmentsByWorkspace and
-- DeleteCalendarEventAttachmentsByWorkspace first, in the same
-- transaction: those two tables reference storage_objects with
-- ON DELETE RESTRICT and would otherwise block this statement depending
-- on the order InnoDB walks the cascade.
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
-- storage_objects rows whose blobs the caller already purged) AND the
-- attachments / calendar_event_attachments rows the user uploaded:
-- both uploader_id FKs are ON DELETE CASCADE, not SET NULL. That is
-- what makes the caller's ref_count decrement obligatory rather than
-- tidy-up — those tables reference storage_objects with ON DELETE
-- RESTRICT, so the counters must already be decremented when the
-- cascade drops the rows, or the sole-referrer sweep that follows
-- cannot tell a freed blob from a shared one.
--
-- Audit-trail back-refs are the ones that survive, and they survive
-- because they are separately declared ON DELETE SET NULL:
-- events.actor_user_id, mcp_invocations.user_id,
-- instance_audit_logs.actor_user_id, tasks.created_by_user_id. The
-- history keeps its shape and loses only the name.
DELETE FROM users
WHERE id = ?;
