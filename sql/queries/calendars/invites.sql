-- name: CreateCalendarEventInvite :execresult
-- Insert a new magic-link invite for a calendar event attendee.
-- accepted_at and sent_at are left NULL by default; the caller uses
-- LastInsertId from the returned sql.Result to follow up with reads.
INSERT INTO calendar_event_invites (
  public_id,
  workspace_id,
  calendar_id,
  event_id,
  attendee_id,
  email,
  token_hash,
  expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindCalendarEventInviteByTokenHash :one
-- Look up an enabled invite by its SHA-256 token hash.
-- Expiry is intentionally NOT filtered here so the handler can
-- distinguish "expired" from "not found" and return clearer errors.
SELECT
  id, public_id, workspace_id, calendar_id, event_id, attendee_id,
  email, token_hash, expires_at, accepted_at, sent_at,
  notes, enabled, updated_at, created_at
FROM calendar_event_invites
WHERE token_hash = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindCalendarEventInviteByPublicId :one
-- Look up an enabled invite by its public UUID.
SELECT
  id, public_id, workspace_id, calendar_id, event_id, attendee_id,
  email, token_hash, expires_at, accepted_at, sent_at,
  notes, enabled, updated_at, created_at
FROM calendar_event_invites
WHERE public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindCalendarEventInviteForAttendee :one
-- Find the invite row for an (event_id, attendee_id) pair whatever its
-- state. UNIQUE(event_id, attendee_id) says there is at most one, ever:
-- an invite is a single standing grant rather than a series, so a
-- revoked one is the same row waiting to be revived, not a tombstone
-- beside which a second may be inserted.
--
-- Deliberately not filtered on enabled. Looking only at live rows made
-- the create path miss the revoked row, insert, collide with it, and
-- fail — which meant a participant could never be invited again after
-- one revocation.
SELECT
  id, public_id, workspace_id, calendar_id, event_id, attendee_id,
  email, token_hash, expires_at, accepted_at, sent_at,
  notes, enabled, updated_at, created_at
FROM calendar_event_invites
WHERE event_id = ?
  AND attendee_id = ?
LIMIT 1;

-- name: ReviveCalendarEventInvite :exec
-- Bring an invite row back into service with a fresh capability:
-- install a new token_hash + expires_at, clear the delivery state, and
-- re-enable the row.
--
-- The token has to be new. Restoring the previous one would make a
-- revocation reversible by whoever still held the old link, so the
-- revive and the rotation are one statement rather than two a caller
-- could get half-right. Rotating a live invite (the resend flow) runs
-- the same statement; enabled = TRUE is simply already true.
UPDATE calendar_event_invites
SET token_hash = ?,
    expires_at = ?,
    sent_at = NULL,
    accepted_at = NULL,
    enabled = TRUE
WHERE id = ?;

-- name: MarkCalendarEventInviteSent :exec
-- Stamp sent_at when the invite email is actually dispatched.
UPDATE calendar_event_invites
SET sent_at = NOW(6)
WHERE id = ?;

-- name: MarkCalendarEventInviteAccepted :exec
-- Stamp accepted_at when the recipient clicks the magic link
-- successfully.
UPDATE calendar_event_invites
SET accepted_at = NOW(6)
WHERE id = ?;

-- name: DisableCalendarEventInvite :exec
-- Soft-disable (revoke) an invite by internal id.
UPDATE calendar_event_invites
SET enabled = FALSE
WHERE id = ?
  AND enabled = TRUE;

-- name: ListCalendarEventInvitesForEvent :many
-- List active invites for a single event, newest first. Paginated
-- (LIMIT/OFFSET) so the result set is always bounded; total carries the
-- pre-page count.
SELECT
  id, public_id, workspace_id, calendar_id, event_id, attendee_id,
  email, token_hash, expires_at, accepted_at, sent_at,
  notes, enabled, updated_at, created_at,
  COUNT(*) OVER() AS total
FROM calendar_event_invites
WHERE event_id = ?
  AND enabled = TRUE
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: ListMyCalendarEventInvites :many
-- Inbox query for /me/invites: active, unaccepted, non-expired invites
-- addressed to the authenticated user's primary email. JOINs event,
-- calendar, and workspace metadata so the handler can build a rich
-- inbox response without extra round trips.
SELECT
  i.public_id,
  i.workspace_id,
  i.calendar_id,
  i.event_id,
  i.attendee_id,
  i.email,
  i.expires_at,
  i.sent_at,
  i.created_at,
  e.public_id AS event_public_id,
  e.title AS event_title,
  e.start_at AS event_start_at,
  e.end_at AS event_end_at,
  e.all_day AS event_all_day,
  e.location AS event_location,
  c.public_id AS calendar_public_id,
  c.name AS calendar_name,
  w.public_id AS workspace_public_id,
  w.name AS workspace_name,
  COUNT(*) OVER() AS total
FROM calendar_event_invites i
INNER JOIN calendar_events e ON e.id = i.event_id AND e.enabled = TRUE
INNER JOIN calendars c ON c.id = i.calendar_id AND c.enabled = TRUE
INNER JOIN workspaces w ON w.id = i.workspace_id
WHERE i.email = ?
  AND i.enabled = TRUE
  AND i.accepted_at IS NULL
  AND i.expires_at > NOW(6)
ORDER BY i.created_at DESC, i.id DESC
LIMIT ? OFFSET ?;

-- name: CleanupExpiredCalendarEventInvites :exec
-- TTL sweep: disable any invite whose expires_at is in the past.
UPDATE calendar_event_invites
SET enabled = FALSE
WHERE expires_at <= NOW(6)
  AND enabled = TRUE;
