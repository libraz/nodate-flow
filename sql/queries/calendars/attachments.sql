-- name: CreateCalendarEventAttachment :execlastid
-- Record per-event attachment metadata. The blob (sha256, byte_size,
-- content_type, storage_key) lives in storage_objects and is referenced via
-- storage_object_id; caller MUST insert the storage_objects row or bump its
-- ref_count inside the same transaction.
INSERT INTO calendar_event_attachments (
  public_id,
  workspace_id,
  event_id,
  uploader_id,
  storage_object_id,
  filename
) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListCalendarEventAttachments :many
-- List active attachments for an event with uploader display fields and the
-- storage_objects metadata flattened in via JOIN. Order is stable by
-- created_at then public_id.
-- The uploader is LEFT JOINed: the file belongs to the event, so suspending
-- the account that uploaded it must not take it off the event for the other
-- attendees. `enabled = TRUE` stays in the ON clause so a suspended
-- uploader's identity is withheld rather than the row disappearing.
SELECT
  a.public_id,
  a.filename,
  so.public_id AS storage_object_public_id,
  so.content_type,
  so.byte_size,
  so.storage_key,
  so.sha256,
  a.uploader_id,
  u.public_id AS user_public_id,
  u.display_name,
  a.created_at
FROM calendar_event_attachments a
LEFT JOIN users u ON u.id = a.uploader_id AND u.enabled = TRUE
INNER JOIN storage_objects so ON so.id = a.storage_object_id AND so.enabled = TRUE
WHERE a.workspace_id = ?
  AND a.event_id = ?
  AND a.enabled = TRUE
ORDER BY a.created_at ASC, a.public_id ASC;

-- name: FindCalendarEventAttachmentByPublicId :one
-- Resolve an attachment by UUID v7 for download or deletion, including the
-- backing storage_objects metadata so the handler can build a presigned URL
-- without a second round trip.
SELECT
  a.id,
  a.public_id,
  a.event_id,
  a.uploader_id,
  a.filename,
  so.id AS storage_object_id,
  so.public_id AS storage_object_public_id,
  so.content_type,
  so.byte_size,
  so.storage_key,
  so.sha256,
  a.enabled,
  a.created_at
FROM calendar_event_attachments a
INNER JOIN storage_objects so ON so.id = a.storage_object_id AND so.enabled = TRUE
WHERE a.workspace_id = ?
  AND a.event_id = ?
  AND a.public_id = ?
  AND a.enabled = TRUE
LIMIT 1;

-- name: DeleteCalendarEventAttachment :execrows
-- Hard-delete a calendar event attachment row. Caller MUST have already
-- decremented ref_count on the linked storage_objects row inside the same
-- transaction; this row holding the FK reference must go away before
-- DeleteStorageObjectIfUnreferenced can free the storage object (FK is
-- ON DELETE RESTRICT). Audit trail survives via events.
DELETE FROM calendar_event_attachments
WHERE workspace_id = ?
  AND event_id = ?
  AND public_id = ?;
