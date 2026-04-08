-- name: ListInbox :many
-- List a workspace's inbox via v_inbox.
SELECT
  v.workspace_id,
  v.workspace_public_id,
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
ORDER BY v.received_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ListInboxForUser :many
-- List inbox items across every workspace the actor is an active member of.
SELECT
  v.workspace_id,
  v.workspace_public_id,
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
INNER JOIN workspace_members wm
  ON wm.workspace_id = v.workspace_id
  AND wm.user_id = ?
  AND wm.enabled = TRUE
ORDER BY v.received_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ArchiveInboxItem :exec
-- Archive a signal by soft-disabling it. The inbox view excludes disabled rows.
UPDATE signals
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;

-- name: SnoozeInboxItem :exec
-- Snooze a signal by pushing its received_at forward. Minimal impl;
-- a dedicated snoozed_until_at column may be added later on.
UPDATE signals
SET received_at = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;
