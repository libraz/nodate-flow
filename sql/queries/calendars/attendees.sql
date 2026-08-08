-- name: CreateCalendarEventAttendee :execlastid
-- Add an attendee to a calendar event.
INSERT INTO calendar_event_attendees (
  public_id,
  workspace_id,
  event_id,
  user_id,
  rsvp,
  can_edit
) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListCalendarEventAttendees :many
-- List all attendees for an event with user profile info.
SELECT
  a.public_id,
  a.user_id,
  a.rsvp,
  a.can_edit,
  u.public_id AS user_public_id,
  u.display_name,
  u.avatar_url,
  a.created_at
FROM calendar_event_attendees a
INNER JOIN users u ON u.id = a.user_id AND u.enabled = TRUE
WHERE a.event_id = ?
  AND a.workspace_id = ?
  AND a.enabled = TRUE
ORDER BY a.sort_weight ASC, a.created_at ASC;

-- name: FindCalendarEventAttendee :one
-- Look up a specific attendee on an event (for permission checks).
SELECT
  id,
  rsvp,
  can_edit,
  enabled
FROM calendar_event_attendees
WHERE event_id = ?
  AND user_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: UpdateAttendeeRsvp :exec
-- Update an attendee's RSVP response (self-service).
UPDATE calendar_event_attendees
SET rsvp = ?
WHERE event_id = ?
  AND user_id = ?
  AND enabled = TRUE;

-- name: UpdateAttendeeCanEdit :exec
-- Grant or revoke edit permission on an attendee (by event owner).
UPDATE calendar_event_attendees
SET can_edit = ?
WHERE event_id = ?
  AND user_id = ?
  AND enabled = TRUE;

-- name: DisableCalendarEventAttendee :exec
-- Remove an attendee from an event (soft-delete).
UPDATE calendar_event_attendees
SET enabled = FALSE
WHERE event_id = ?
  AND user_id = ?
  AND enabled = TRUE;

-- name: DeleteAllCalendarEventAttendees :exec
-- Remove all attendees from an event (used when re-setting attendee list).
UPDATE calendar_event_attendees
SET enabled = FALSE
WHERE event_id = ?;
