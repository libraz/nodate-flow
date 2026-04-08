-- name: AddAttachment :execlastid
-- Insert metadata for a newly uploaded attachment.
INSERT INTO attachments (
  public_id,
  workspace_id,
  task_id,
  uploader_id,
  filename,
  content_type,
  byte_size,
  storage_key,
  checksum_sha256
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAttachmentsForTask :many
-- List attachments on a task with uploader display fields.
SELECT
  a.public_id,
  u.public_id AS uploader_public_id,
  u.display_name AS uploader_display_name,
  a.filename,
  a.content_type,
  a.byte_size,
  a.storage_key,
  a.checksum_sha256,
  a.updated_at,
  a.created_at,
  COUNT(*) OVER() AS total
FROM attachments a
INNER JOIN tasks t ON t.id = a.task_id AND t.enabled = TRUE
INNER JOIN users u ON u.id = a.uploader_id AND u.enabled = TRUE
WHERE a.workspace_id = ?
  AND t.public_id = ?
  AND a.enabled = TRUE
ORDER BY a.created_at DESC, a.public_id DESC
LIMIT ? OFFSET ?;

-- name: DeleteAttachment :exec
-- Soft-delete an attachment row. Object storage cleanup is async.
UPDATE attachments
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;
