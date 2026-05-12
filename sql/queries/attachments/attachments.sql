-- name: AddAttachment :execlastid
-- Insert per-task attachment metadata. The blob itself (sha256, byte_size,
-- content_type, storage_key) is owned by storage_objects and looked up via
-- storage_object_id; caller MUST have either inserted a fresh storage_objects
-- row or bumped ref_count on an existing one inside the same transaction.
INSERT INTO attachments (
  public_id,
  workspace_id,
  task_id,
  uploader_id,
  storage_object_id,
  filename
) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListAttachmentsForTask :many
-- List attachments on a task with uploader display fields and the
-- storage_objects metadata flattened in via JOIN. Returns total via a
-- window function for paginated UI.
SELECT
  a.public_id,
  u.public_id AS uploader_public_id,
  u.display_name AS uploader_display_name,
  a.filename,
  so.public_id AS storage_object_public_id,
  so.content_type,
  so.byte_size,
  so.storage_key,
  so.sha256,
  a.updated_at,
  a.created_at,
  COUNT(*) OVER() AS total
FROM attachments a
INNER JOIN tasks t ON t.id = a.task_id AND t.enabled = TRUE
INNER JOIN users u ON u.id = a.uploader_id AND u.enabled = TRUE
INNER JOIN storage_objects so ON so.id = a.storage_object_id AND so.enabled = TRUE
WHERE a.workspace_id = ?
  AND t.public_id = ?
  AND a.enabled = TRUE
ORDER BY a.created_at DESC, a.public_id DESC
LIMIT ? OFFSET ?;

-- name: GetAttachmentByPublicID :one
-- Fetch a single attachment by its public id within a workspace, with the
-- backing storage_objects metadata. Used by download / detail handlers.
SELECT
  a.public_id,
  a.filename,
  so.id AS storage_object_id,
  so.public_id AS storage_object_public_id,
  so.content_type,
  so.byte_size,
  so.storage_key,
  so.sha256,
  a.updated_at,
  a.created_at
FROM attachments a
INNER JOIN storage_objects so ON so.id = a.storage_object_id AND so.enabled = TRUE
WHERE a.workspace_id = ?
  AND a.public_id = ?
  AND a.enabled = TRUE;

-- name: GetAttachmentStorageObjectIDForDelete :one
-- Resolve the internal storage_object_id for an attachment so the delete
-- handler can decrement ref_count (and possibly GC the underlying blob)
-- in the same transaction as the hard-delete below. Returns the internal
-- attachment id as well so the caller does not have to round-trip.
SELECT
  a.id,
  a.storage_object_id
FROM attachments a
WHERE a.workspace_id = ?
  AND a.public_id = ?
  AND a.enabled = TRUE
LIMIT 1;

-- name: DeleteAttachment :exec
-- Hard-delete an attachment row. Caller MUST have already
-- decremented ref_count on the linked storage_objects row inside
-- the same transaction; this row holding the FK reference must go
-- away before DeleteStorageObjectIfUnreferenced can free the storage
-- object (FK is ON DELETE RESTRICT). Audit trail survives via events.
DELETE FROM attachments
WHERE workspace_id = ?
  AND public_id = ?;
