-- name: CreateCalendarEventComment :execlastid
-- Add a comment to a calendar event.
INSERT INTO calendar_event_comments (
  public_id,
  workspace_id,
  event_id,
  author_id,
  body
) VALUES (?, ?, ?, ?, ?);

-- name: ListCalendarEventComments :many
-- List comments on an event in chronological order.
SELECT
  c.public_id,
  c.author_id,
  c.body,
  c.edited_at,
  u.public_id AS user_public_id,
  u.display_name,
  u.avatar_url,
  c.created_at
FROM calendar_event_comments c
INNER JOIN users u ON u.id = c.author_id AND u.enabled = TRUE
WHERE c.event_id = ?
  AND c.enabled = TRUE
ORDER BY c.created_at ASC;

-- name: FindCalendarEventCommentByPublicId :one
-- Resolve a comment by UUID v7.
SELECT
  id,
  public_id,
  event_id,
  author_id,
  body,
  edited_at,
  enabled,
  created_at
FROM calendar_event_comments
WHERE public_id = ?
  AND event_id = ?
  AND workspace_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: UpdateCalendarEventComment :exec
-- Edit a comment's body and stamp edited_at.
UPDATE calendar_event_comments
SET body = ?,
    edited_at = NOW()
WHERE public_id = ?
  AND event_id = ?
  AND workspace_id = ?
  AND author_id = ?
  AND enabled = TRUE;

-- name: DisableCalendarEventComment :exec
-- Soft-delete a comment (author or calendar owner).
UPDATE calendar_event_comments
SET enabled = FALSE
WHERE public_id = ?
  AND event_id = ?
  AND workspace_id = ?;
