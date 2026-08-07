-- name: CreateCalendarMemo :execlastid
-- Add a shared memo/to-do item to a calendar.
INSERT INTO calendar_memos (
  public_id,
  workspace_id,
  calendar_id,
  created_by_user_id,
  title,
  body,
  sort_weight
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListCalendarMemos :many
-- List memos for a calendar in display order.
-- The creator is LEFT JOINed: a memo belongs to the shared calendar, so
-- suspending the account that wrote it must not remove it from the calendar
-- everyone else is reading. `enabled = TRUE` stays in the ON clause so a
-- suspended creator's identity is withheld rather than the memo
-- disappearing.
SELECT
  m.public_id,
  m.title,
  m.body,
  m.done,
  m.sort_weight,
  m.created_by_user_id,
  u.public_id AS user_public_id,
  u.display_name,
  m.updated_at,
  m.created_at
FROM calendar_memos m
LEFT JOIN users u ON u.id = m.created_by_user_id AND u.enabled = TRUE
WHERE m.calendar_id = ?
  AND m.workspace_id = ?
  AND m.enabled = TRUE
ORDER BY m.sort_weight ASC, m.created_at ASC;

-- name: FindCalendarMemoByPublicId :one
-- Resolve a memo by UUID v7.
SELECT
  id,
  public_id,
  calendar_id,
  title,
  body,
  done,
  sort_weight,
  created_by_user_id,
  enabled,
  updated_at,
  created_at
FROM calendar_memos
WHERE public_id = ?
  AND calendar_id = ?
  AND workspace_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: UpdateCalendarMemo :exec
-- Update a memo's title, done, or sort_weight.
UPDATE calendar_memos
SET title       = COALESCE(sqlc.narg('title'), title),
    body        = COALESCE(sqlc.narg('body'), body),
    done        = COALESCE(sqlc.narg('done'), done),
    sort_weight = COALESCE(sqlc.narg('sort_weight'), sort_weight)
WHERE public_id = ?
  AND calendar_id = ?
  AND workspace_id = ?
  AND enabled = TRUE;

-- name: DisableCalendarMemo :exec
-- Soft-delete a memo.
UPDATE calendar_memos
SET enabled = FALSE
WHERE public_id = ?
  AND calendar_id = ?
  AND workspace_id = ?;
