-- name: CreateCalendarEventAttachment :execlastid
-- Record metadata for an uploaded file attachment on a calendar event.
INSERT INTO calendar_event_attachments (
  public_id,
  workspace_id,
  event_id,
  uploader_id,
  filename,
  content_type,
  byte_size,
  storage_key,
  checksum_sha256
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListCalendarEventAttachments :many
-- List active attachments for an event.
SELECT
  a.public_id,
  a.filename,
  a.content_type,
  a.byte_size,
  a.storage_key,
  a.uploader_id,
  u.public_id AS user_public_id,
  u.display_name,
  a.created_at
FROM calendar_event_attachments a
INNER JOIN users u ON u.id = a.uploader_id AND u.enabled = TRUE
WHERE a.event_id = ?
  AND a.enabled = TRUE
ORDER BY a.created_at ASC;

-- name: FindCalendarEventAttachmentByPublicId :one
-- Resolve an attachment by UUID v7 for download or deletion.
SELECT
  id,
  public_id,
  event_id,
  uploader_id,
  filename,
  content_type,
  byte_size,
  storage_key,
  enabled,
  created_at
FROM calendar_event_attachments
WHERE public_id = ?
  AND event_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: DisableCalendarEventAttachment :exec
-- Soft-delete an attachment. Actual blob cleanup is deferred.
UPDATE calendar_event_attachments
SET enabled = FALSE
WHERE public_id = ?
  AND event_id = ?;
