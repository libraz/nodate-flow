-- name: InsertSignal :execlastid
-- Insert an inbound signal (manual or webhook).
-- Dedup is workspace-scoped via UNIQUE (workspace_id, source, external_id)
-- when external_id is non-NULL. Duplicate deliveries are silently ignored
-- via INSERT IGNORE; LastInsertId() returns 0 when the row was a duplicate.
INSERT IGNORE INTO signals (
  public_id,
  workspace_id,
  task_id,
  source,
  kind,
  external_id,
  payload_json,
  received_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListSignalsForTask :many
-- List signals attached to a task via v_inbox.
SELECT
  v.public_id,
  v.task_public_id,
  v.task_title,
  v.source,
  v.kind,
  v.external_id,
  v.payload_json,
  v.received_at,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_inbox v
WHERE v.workspace_id = ?
  AND v.task_public_id = ?
ORDER BY v.received_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ListUnattachedSignals :many
-- List signals in a workspace that have no task linkage yet.
SELECT
  v.public_id,
  v.source,
  v.kind,
  v.external_id,
  v.payload_json,
  v.received_at,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_inbox v
WHERE v.workspace_id = ?
  AND v.task_public_id IS NULL
ORDER BY v.received_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: AttachSignalToTask :exec
-- Link an existing signal to a task by public_id.
UPDATE signals s
SET s.task_id = (
  SELECT t.id FROM tasks t
  WHERE t.workspace_id = ? AND t.public_id = ? AND t.enabled = TRUE
)
WHERE s.workspace_id = ?
  AND s.public_id = ?
  AND s.enabled = TRUE;
