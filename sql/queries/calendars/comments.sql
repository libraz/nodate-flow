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
-- List comments on an event in chronological order. Paginated
-- (LIMIT/OFFSET) so the result set is always bounded; total carries the
-- pre-page count.
-- The author is LEFT JOINed: a comment is part of the event's discussion,
-- so suspending the account that wrote it must not erase what was said for
-- the rest of the attendees. `enabled = TRUE` stays in the ON clause so a
-- suspended author's identity is withheld rather than the comment
-- disappearing.
SELECT
  c.public_id,
  c.author_id,
  c.body,
  c.edited_at,
  u.public_id AS user_public_id,
  u.display_name,
  u.avatar_url,
  c.created_at,
  COUNT(*) OVER() AS total
FROM calendar_event_comments c
LEFT JOIN users u ON u.id = c.author_id AND u.enabled = TRUE
WHERE c.event_id = ?
  AND c.workspace_id = ?
  AND c.enabled = TRUE
  AND c.deleted_at IS NULL
ORDER BY c.created_at ASC, c.public_id ASC
LIMIT ? OFFSET ?;

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
  AND deleted_at IS NULL
LIMIT 1;

-- name: UpdateCalendarEventComment :execrows
-- Edit a comment's body and stamp edited_at.
UPDATE calendar_event_comments
SET body = ?,
    edited_at = NOW()
WHERE public_id = ?
  AND event_id = ?
  AND workspace_id = ?
  AND author_id = ?
  AND enabled = TRUE
  AND deleted_at IS NULL;

-- name: DisableCalendarEventComment :execrows
-- Soft-delete a comment (author or calendar owner).
UPDATE calendar_event_comments
SET enabled = FALSE,
    deleted_at = CURRENT_TIMESTAMP(3)
WHERE public_id = ?
  AND event_id = ?
  AND workspace_id = ?
  AND deleted_at IS NULL;
