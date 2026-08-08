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
--
-- a.id is selected because callers that join attendees back to invites
-- (calendar_event_invites.attendee_id is an internal FK) otherwise have
-- to re-look-up every attendee one at a time, which turns a single
-- large-event request into one round trip per attendee. sqlc tags it
-- json:"-" via the *.id override, so it stays off the API boundary.
SELECT
  a.id,
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
--
-- The (event_id, user_id) pair is the whole scope, and it can legitimately
-- match nothing: being able to see an event is not being invited to it, so
-- an actor who never appears on the attendee list has no RSVP to change.
-- Callers that need to know establish attendance with FindCalendarEventAttendee
-- first; an affected-row count cannot answer it. The connection does not set
-- CLIENT_FOUND_ROWS, so the count reports changed rows, not matched ones, and
-- re-submitting the RSVP already on file is indistinguishable from a missing
-- row. (:execrows fits the atomic-claim queries elsewhere in the repo for the
-- opposite reason: their WHERE clause lets at most one caller change anything.)
UPDATE calendar_event_attendees
SET rsvp = ?
WHERE event_id = ?
  AND user_id = ?
  AND enabled = TRUE;

-- name: UpdateAttendeeCanEdit :exec
-- Grant or revoke edit permission on an attendee (by event owner).
--
-- Same scope, same open outcome, but about the target rather than the actor:
-- the user named in the request may hold no live attendee row on this event,
-- and a grant written against nothing is a permission the owner believes they
-- handed out. Confirm the target with FindCalendarEventAttendee, not with an
-- affected-row count -- re-granting an attendee the edit rights they already
-- hold changes no row and so counts the same as a target that is not there.
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
