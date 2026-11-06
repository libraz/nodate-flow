-- name: CreateIntakeItem :execlastid
-- Insert a new intake item into the triage queue.
INSERT INTO intake_items (
  public_id, workspace_id, signal_id, title, body
) VALUES (?, ?, ?, ?, ?);

-- name: CreateIntakeItemForSignal :execresult
-- Capture one source signal at most once per workspace. MySQL permits
-- multiple NULL signal ids, so manual intake rows remain independent.
INSERT INTO intake_items (
  public_id, workspace_id, signal_id, title, body, source_url
) VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id);

-- name: FindIntakeItemBySignal :one
-- Resolve and lock the one intake row associated with a source signal.
SELECT
  ii.id,
  ii.public_id,
  ii.workspace_id,
  ii.signal_id,
  ii.task_id,
  ii.title,
  ii.body,
  ii.source_url,
  ii.triage_status,
  ii.snooze_until,
  ii.created_at
FROM intake_items ii
WHERE ii.workspace_id = ?
  AND ii.signal_id = ?
  AND ii.enabled = TRUE
FOR UPDATE;

-- name: LinkIntakeItemToExistingTask :execrows
-- Claim an unconverted intake by linking it to an already-visible task. The
-- status predicate is SetIntakeItemTask's: pending, accepted without a task,
-- or snoozed past its deadline; a rejected or duplicate item is not reopened.
UPDATE intake_items
SET task_id = ?,
    triage_status = 'accepted',
    triaged_by_user_id = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
  AND task_id IS NULL
  AND (triage_status IN ('pending', 'accepted')
       OR (triage_status = 'snoozed' AND snooze_until <= NOW(3)));

-- name: FindPublicIdForIntakeTask :one
-- Resolve the public task id for a converted intake's internal foreign key.
SELECT public_id
FROM tasks
WHERE workspace_id = ?
  AND id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: ListTaskIntakeSources :many
-- Follow a task's enabled intake backlinks to their bounded source details.
-- Source payload JSON is deliberately not returned: its display projection
-- was captured into the intake row after HTTP(S)-only URL validation.
SELECT
  ii.public_id,
  COALESCE(BIN_TO_UUID(s.public_id, 0), '') AS signal_public_id,
  COALESCE(s.source, '') AS source,
  COALESCE(s.kind, '') AS kind,
  ii.title,
  LEFT(COALESCE(ii.body, ''), 50000) AS body,
  COALESCE(ii.source_url, '') AS source_url,
  ii.created_at
FROM intake_items ii
LEFT JOIN signals s
  ON s.id = ii.signal_id
 AND s.workspace_id = ii.workspace_id
WHERE ii.workspace_id = ?
  AND ii.task_id = ?
  AND ii.enabled = TRUE
ORDER BY ii.created_at ASC, ii.id ASC
LIMIT 50;

-- name: ListIntakeItemsForWorkspace :many
-- List intake items for a workspace, filtered by status. A snooze whose
-- deadline has passed is pending again: it never lists under snoozed, and
-- include_expired_snoozes (1 when the filter is pending) lists it there.
SELECT
  ii.public_id,
  ii.workspace_id,
  ii.title,
  ii.body,
  ii.source_url,
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
  AND (sqlc.arg(status_filter) = ''
       OR (ii.triage_status = sqlc.arg(status_filter)
           AND NOT (ii.triage_status = 'snoozed' AND ii.snooze_until <= NOW(3)))
       OR (CAST(sqlc.arg('include_expired_snoozes') AS SIGNED) = 1
           AND ii.triage_status = 'snoozed' AND ii.snooze_until <= NOW(3)))
ORDER BY ii.created_at DESC
LIMIT ? OFFSET ?;

-- name: ListIntakeItemsForWorkspaceKeyset :many
-- Cursor-paginated intake items for a workspace, filtered by status, with
-- expired snoozes listed under pending as in ListIntakeItemsForWorkspace.
SELECT
  ii.public_id,
  ii.workspace_id,
  ii.title,
  ii.body,
  ii.source_url,
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
  AND (sqlc.arg(status_filter) = ''
       OR (ii.triage_status = sqlc.arg(status_filter)
           AND NOT (ii.triage_status = 'snoozed' AND ii.snooze_until <= NOW(3)))
       OR (CAST(sqlc.arg('include_expired_snoozes') AS SIGNED) = 1
           AND ii.triage_status = 'snoozed' AND ii.snooze_until <= NOW(3)))
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
  ii.signal_id,
  ii.title,
  ii.body,
  ii.source_url,
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

-- name: FindIntakeItemForConversion :one
-- Lock the current intake state after its source signal has been locked.
SELECT
  ii.id,
  ii.public_id,
  ii.workspace_id,
  ii.signal_id,
  ii.title,
  ii.body,
  ii.source_url,
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
  AND ii.enabled = TRUE
LIMIT 1
FOR UPDATE;

-- name: FindSignalPublicIdForIntakeItem :one
-- Resolve the source signal's public id while writing conversion events.
SELECT public_id
FROM signals
WHERE workspace_id = ?
  AND id = ?
LIMIT 1;

-- name: UpdateIntakeItemTriage :execrows
-- Triage an intake item out of pending. The predicate is the claim: only an
-- unconverted item that is pending, or snoozed past its deadline, matches,
-- so of two racing triages exactly one counts a row.
UPDATE intake_items
SET triage_status      = ?,
    triaged_by_user_id = ?,
    snooze_until       = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
  AND task_id IS NULL
  AND (triage_status = 'pending'
       OR (triage_status = 'snoozed' AND snooze_until <= NOW(3)));

-- name: SetIntakeItemTask :execrows
-- Link an intake item to a converted task. Matches only an item not yet
-- converted, so a losing concurrent conversion counts zero rows and rolls
-- its task back. Beyond UpdateIntakeItemTriage's claim it also accepts an
-- item triaged as accepted, which the triage route forwards to conversion.
UPDATE intake_items
SET task_id        = ?,
    triage_status  = 'accepted'
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
  AND task_id IS NULL
  AND (triage_status IN ('pending', 'accepted')
       OR (triage_status = 'snoozed' AND snooze_until <= NOW(3)));
