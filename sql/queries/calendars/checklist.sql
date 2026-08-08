-- name: CreateCalendarChecklistItem :execlastid
-- Add a checklist item to a calendar event.
INSERT INTO calendar_event_checklist_items (
  public_id,
  workspace_id,
  event_id,
  created_by_user_id,
  title,
  sort_weight
) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListCalendarChecklistItems :many
-- List checklist items for an event in display order. Paginated
-- (LIMIT/OFFSET) so the result set is always bounded; total carries the
-- pre-page count.
SELECT
  public_id,
  title,
  done,
  sort_weight,
  created_by_user_id,
  created_at,
  COUNT(*) OVER() AS total
FROM calendar_event_checklist_items
WHERE event_id = ?
  AND workspace_id = ?
  AND enabled = TRUE
ORDER BY sort_weight ASC, created_at ASC, public_id ASC
LIMIT ? OFFSET ?;

-- name: FindCalendarChecklistItemByPublicId :one
-- Resolve a checklist item by UUID v7.
SELECT
  id,
  public_id,
  event_id,
  title,
  done,
  sort_weight,
  created_by_user_id,
  enabled,
  created_at
FROM calendar_event_checklist_items
WHERE public_id = ?
  AND event_id = ?
  AND workspace_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: UpdateCalendarChecklistItem :execrows
-- Update a checklist item's title, done, or sort_weight.
UPDATE calendar_event_checklist_items
SET title       = COALESCE(sqlc.narg('title'), title),
    done        = COALESCE(sqlc.narg('done'), done),
    sort_weight = COALESCE(sqlc.narg('sort_weight'), sort_weight)
WHERE public_id = ?
  AND event_id = ?
  AND workspace_id = ?
  AND enabled = TRUE;

-- name: DisableCalendarChecklistItem :execrows
-- Soft-delete a checklist item.
UPDATE calendar_event_checklist_items
SET enabled = FALSE
WHERE public_id = ?
  AND event_id = ?
  AND workspace_id = ?
  AND enabled = TRUE;
