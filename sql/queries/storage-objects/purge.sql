-- name: ListStorageObjectsByWorkspace :many
-- Enumerate every storage_objects row scoped to a workspace, including
-- already soft-disabled rows, so the admin purge driver can stream them
-- to the MinIO sweeper before the workspace row is hard-deleted (the
-- ON DELETE CASCADE on storage_objects.workspace_id would otherwise
-- orphan the underlying blobs).
--
-- Pre-conditions: caller has already verified the workspace is
-- soft-deleted (enabled = FALSE). No workspace_id-on-WHERE-line-1 rule
-- here because purge IS the admin tooling that operates across the
-- workspace boundary by design.
SELECT
  id,
  storage_key
FROM storage_objects
WHERE workspace_id = ?
ORDER BY id ASC;

-- name: ListStorageObjectsByOwnerUser :many
-- Enumerate every storage_objects row owned directly by a user (avatars
-- only at the moment) so the admin purge driver can clean MinIO before
-- the users row is hard-deleted. Workspace-scoped attachments uploaded
-- by the same user live under workspace_id and are handled separately
-- via ListAttachmentsForUploaderPurge / the calendar variant.
SELECT
  id,
  storage_key
FROM storage_objects
WHERE owner_user_id = ?
ORDER BY id ASC;

-- name: ListAttachmentsForUploaderPurge :many
-- Enumerate task attachments uploaded by a user across every workspace
-- they belong to, joined with the underlying storage_objects so the
-- purge driver can decrement ref_count and (if ref_count reaches 0)
-- GC the MinIO blob. The workspace owning the attachment may stay
-- alive after the user is deleted, so this cannot rely on
-- ON DELETE CASCADE alone.
--
-- The current ref_count column is returned so the caller can short-
-- circuit the GC decision without an extra round-trip; treat it as a
-- hint and re-check inside the decrement transaction.
SELECT
  a.id,
  a.storage_object_id,
  so.storage_key,
  so.ref_count
FROM attachments a
JOIN storage_objects so ON so.id = a.storage_object_id
WHERE a.uploader_id = ?
ORDER BY a.id ASC;

-- name: ListCalendarEventAttachmentsForUploaderPurge :many
-- Calendar-event variant of ListAttachmentsForUploaderPurge. Same
-- contract: enumerate every calendar_event_attachments row uploaded
-- by the target user with the joined storage_objects.storage_key and
-- ref_count so the purge driver can decrement and GC blobs whose last
-- referencing row is being removed.
SELECT
  a.id,
  a.storage_object_id,
  so.storage_key,
  so.ref_count
FROM calendar_event_attachments a
JOIN storage_objects so ON so.id = a.storage_object_id
WHERE a.uploader_id = ?
ORDER BY a.id ASC;
