-- name: CreateCalendarInvite :execlastid
-- Create a shareable invite link for a calendar.
INSERT INTO calendar_invites (
  public_id,
  workspace_id,
  calendar_id,
  created_by_user_id,
  token_hash,
  role,
  max_uses,
  expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindCalendarInviteByTokenHash :one
-- Resolve an invite by its token hash for the acceptance flow.
SELECT
  i.id,
  i.public_id,
  i.workspace_id,
  i.calendar_id,
  i.token_hash,
  i.role,
  i.max_uses,
  i.use_count,
  i.expires_at,
  c.public_id AS calendar_public_id,
  c.name AS calendar_name,
  c.kind AS calendar_kind,
  c.color AS calendar_color,
  i.created_at
FROM calendar_invites i
INNER JOIN calendars c ON c.id = i.calendar_id AND c.enabled = TRUE
WHERE i.token_hash = ?
  AND i.enabled = TRUE
LIMIT 1;

-- name: ListCalendarInvites :many
-- List active invites for a calendar.
SELECT
  public_id,
  token_hash,
  role,
  max_uses,
  use_count,
  expires_at,
  created_at
FROM calendar_invites
WHERE calendar_id = ?
  AND workspace_id = ?
  AND enabled = TRUE
ORDER BY created_at DESC;

-- name: IncrementCalendarInviteUseCount :exec
-- Bump the use counter after a successful acceptance.
UPDATE calendar_invites
SET use_count = use_count + 1
WHERE id = ?;

-- name: DisableCalendarInvite :exec
-- Revoke an invite link.
UPDATE calendar_invites
SET enabled = FALSE
WHERE public_id = ?
  AND calendar_id = ?
  AND workspace_id = ?;

-- name: FindCalendarInviteByTokenHashPublic :one
-- Public-facing invite lookup (for share page preview, no auth required).
SELECT
  c.public_id AS calendar_public_id,
  c.name AS calendar_name,
  c.kind AS calendar_kind,
  c.color AS calendar_color,
  i.role,
  i.expires_at,
  (SELECT COUNT(*) FROM calendar_subscriptions cs
   WHERE cs.calendar_id = c.id AND cs.enabled = TRUE) AS member_count
FROM calendar_invites i
INNER JOIN calendars c ON c.id = i.calendar_id AND c.enabled = TRUE
WHERE i.token_hash = ?
  AND i.enabled = TRUE
  AND (i.expires_at IS NULL OR i.expires_at > NOW())
  AND (i.max_uses IS NULL OR i.use_count < i.max_uses)
LIMIT 1;
