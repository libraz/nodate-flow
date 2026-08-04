-- name: UpsertCalendarMember :execlastid
-- Grant (or re-grant) a user access to a calendar at a given role.
--
-- Upsert rather than insert because uniq_calendar_members_calendar_user
-- covers revoked rows too: re-adding someone who was removed has to
-- revive their row, and letting a second row accumulate would leave an
-- older grant behind for an access check to find.
INSERT INTO calendar_members (
  public_id,
  workspace_id,
  calendar_id,
  user_id,
  role,
  member_color,
  invited_by_user_id
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  role               = VALUES(role),
  member_color       = VALUES(member_color),
  invited_by_user_id = VALUES(invited_by_user_id),
  enabled            = TRUE;

-- name: FindCalendarMember :one
-- Resolve one user's access to a calendar. This is the query every
-- calendar authorization check runs through; sql.ErrNoRows means no
-- access, which callers map to a 404 rather than a 403 so the existence
-- of a calendar is not leaked to someone with no grant on it.
SELECT
  id,
  public_id,
  workspace_id,
  calendar_id,
  user_id,
  role,
  member_color,
  created_at
FROM calendar_members
WHERE calendar_id = ?
  AND user_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: ListCalendarMembers :many
-- List a calendar's members with the role and shared colour each holds.
SELECT
  cm.public_id,
  cm.user_id,
  cm.role,
  cm.member_color,
  cm.sort_weight,
  u.public_id AS user_public_id,
  u.display_name,
  u.avatar_url,
  cm.created_at
FROM calendar_members cm
INNER JOIN users u ON u.id = cm.user_id AND u.enabled = TRUE
WHERE cm.calendar_id = ?
  AND cm.workspace_id = ?
  AND cm.enabled = TRUE
ORDER BY cm.sort_weight ASC, cm.created_at ASC;

-- name: CountCalendarMembers :one
-- Number of live members, used to pick the next colour from the palette.
SELECT COUNT(*)
FROM calendar_members
WHERE calendar_id = ?
  AND workspace_id = ?
  AND enabled = TRUE;

-- name: UpdateCalendarMemberRole :execresult
-- Change a member's role. RowsAffected = 0 means the target holds no live
-- membership, which the caller maps to a 404 rather than silently
-- reporting success.
UPDATE calendar_members
SET role = ?
WHERE calendar_id = ?
  AND user_id = ?
  AND enabled = TRUE;

-- name: CountCalendarOwners :one
-- Live owners of a calendar. Callers check this before demoting or
-- removing an owner, so a calendar cannot be left with nobody able to
-- manage its membership.
SELECT COUNT(*)
FROM calendar_members
WHERE calendar_id = ?
  AND role = 'owner'
  AND enabled = TRUE;

-- name: DisableCalendarMember :execresult
-- Revoke a membership. The row survives so the grant history stays
-- readable and so a later re-add updates it in place.
UPDATE calendar_members
SET enabled = FALSE
WHERE calendar_id = ?
  AND user_id = ?
  AND enabled = TRUE;
