-- Input for kindscan's own tests, not a query this repository runs. Each
-- statement is a shape the rule has to answer for, paired with the same
-- shape written correctly, so a rule that reports everything fails here
-- as loudly as one that reports nothing.
--
-- The kind named in this comment, nothing.declares.this, is not a finding:
-- a comment is not a column position.

-- name: InsertDeclared :execlastid
INSERT INTO events (
  public_id,
  workspace_id,
  type,
  payload_json
) VALUES (?, ?, 'task.created', ?);

-- name: InsertUndeclared :execlastid
INSERT INTO events (
  public_id,
  workspace_id,
  type,
  payload_json
) VALUES (?, ?, 'task.invented', ?);

-- name: InsertNotificationUndeclared :execlastid
INSERT INTO notifications (user_id, event_type) VALUES (?, 'notification.invented');

-- name: InsertNotificationBothTypeColumns :execlastid
-- notifications carries event_type and resource_type on adjacent lines,
-- so one statement names both. The kind is checked; the resource is a
-- plain word and must be left alone, which is what a rule loosened to a
-- _type suffix would get wrong first.
INSERT INTO notifications (
  user_id,
  event_type,
  resource_type
) VALUES (?, 'task.comment.added', 'task');

-- name: SelectNotificationsBothTypeColumns :many
SELECT id FROM notifications
WHERE event_type = 'mention.created'
  AND resource_type = 'task';

-- name: InsertSanctionedUndeclared :execlastid
-- A kind deliberately outside the registry, marked as such.
INSERT INTO events (public_id, type) VALUES (?, 'fixture.deliberate'); -- kindscan:undeclared

-- name: SelectByType :many
SELECT id FROM events
WHERE type = 'task.updated'
  AND type != 'task.imagined'
  AND type IN ('task.disabled', 'task.dreamt')
  AND type LIKE 'task.transition.%'
  AND type LIKE 'task.hallucinated.%';

-- name: SelectByOtherColumn :many
SELECT id FROM audit_logs
WHERE resource_type = 'resource.invented'
  AND action = 'action.invented';

-- name: UpdateType :exec
UPDATE events SET type = 'task.misfiled' WHERE id = ?;
