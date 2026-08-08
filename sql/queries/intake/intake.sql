-- name: CreateIntakeItem :execlastid
-- Insert a new intake item into the triage queue.
INSERT INTO intake_items (
  public_id, workspace_id, signal_id, title, body
) VALUES (?, ?, ?, ?, ?);

-- name: ListIntakeItemsForWorkspace :many
-- List intake items for a workspace, filtered by status.
SELECT
  ii.public_id,
  ii.workspace_id,
  ii.title,
  ii.body,
  ii.triage_status,
  ii.snooze_until,
  ii.ai_score,
  ii.ai_reasoning,
  ii.scored_at,
  ii.task_id,
  ii.triaged_by_user_id,
  tu.public_id AS triaged_by_public_id,
  tu.display_name AS triaged_by_display_name,
  ii.created_at,
  COUNT(*) OVER() AS total
FROM intake_items ii
LEFT JOIN users tu ON tu.id = ii.triaged_by_user_id
WHERE ii.workspace_id = ?
  AND ii.enabled = TRUE
  AND (sqlc.arg(status_filter) = '' OR ii.triage_status = sqlc.arg(status_filter))
ORDER BY ii.created_at DESC
LIMIT ? OFFSET ?;

-- name: ListIntakeItemsForWorkspaceKeyset :many
-- Cursor-paginated intake items for a workspace, filtered by status.
SELECT
  ii.public_id,
  ii.workspace_id,
  ii.title,
  ii.body,
  ii.triage_status,
  ii.snooze_until,
  ii.ai_score,
  ii.ai_reasoning,
  ii.scored_at,
  ii.task_id,
  ii.triaged_by_user_id,
  tu.public_id AS triaged_by_public_id,
  tu.display_name AS triaged_by_display_name,
  ii.created_at
FROM intake_items ii
LEFT JOIN users tu ON tu.id = ii.triaged_by_user_id
WHERE ii.workspace_id = ?
  AND ii.enabled = TRUE
  AND (sqlc.arg(status_filter) = '' OR ii.triage_status = sqlc.arg(status_filter))
  AND (
    sqlc.narg(cursor_created_at) IS NULL
    OR ii.created_at < sqlc.narg(cursor_created_at)
    OR (ii.created_at = sqlc.narg(cursor_created_at) AND ii.public_id < sqlc.arg(cursor_public_id))
  )
ORDER BY ii.created_at DESC, ii.public_id DESC
LIMIT ?;

-- name: FindIntakeItemByPublicId :one
-- Find a single intake item by public id.
SELECT
  ii.id,
  ii.public_id,
  ii.workspace_id,
  ii.title,
  ii.body,
  ii.triage_status,
  ii.snooze_until,
  ii.ai_score,
  ii.ai_reasoning,
  ii.scored_at,
  ii.task_id,
  ii.triaged_by_user_id,
  ii.created_at
FROM intake_items ii
WHERE ii.workspace_id = ?
  AND ii.public_id = ?
  AND ii.enabled = TRUE;

-- name: UpdateIntakeItemTriage :execrows
-- Update the triage status of an intake item.
UPDATE intake_items
SET triage_status      = ?,
    triaged_by_user_id = ?,
    snooze_until       = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: SetIntakeItemTask :execrows
-- Link an intake item to a converted task.
UPDATE intake_items
SET task_id        = ?,
    triage_status  = 'accepted'
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: SetIntakeItemAIScore :execrows
-- Set AI score and reasoning for an intake item.
UPDATE intake_items
SET ai_score     = ?,
    ai_reasoning = ?,
    scored_at    = NOW()
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;
