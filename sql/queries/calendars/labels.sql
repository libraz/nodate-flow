-- name: ListCalendarLabels :many
-- Every label of a calendar, oldest first. The order matches nodate-time so
-- both products list a calendar's labels the same way; sort_weight is not
-- exposed yet and ties would break on id anyway.
SELECT
  id,
  public_id,
  name,
  color,
  updated_at,
  created_at
FROM calendar_labels
WHERE calendar_id = ?
ORDER BY id ASC
LIMIT 500;

-- name: CountCalendarLabels :one
-- Number of labels a calendar holds. Called under LockCalendarForLabelWrite
-- so the cap check and the insert that follows it cannot interleave.
SELECT COUNT(*)
FROM calendar_labels
WHERE calendar_id = ?;

-- name: LockCalendarForLabelWrite :one
-- Serialise label writes on one calendar by locking its row. The label cap
-- and name uniqueness are checked before the write, and two concurrent
-- writers would both pass that check without this lock.
SELECT id
FROM calendars
WHERE id = ?
  AND workspace_id = ?
FOR UPDATE;

-- name: FindCalendarLabelByPublicId :one
-- Resolve a label by UUID v7 within one calendar. Both keys are required so
-- a label of another calendar is never picked up by its public id.
SELECT
  id,
  public_id,
  name,
  color,
  updated_at,
  created_at
FROM calendar_labels
WHERE public_id = ?
  AND calendar_id = ?
LIMIT 1;

-- name: CreateCalendarLabel :execlastid
-- Insert a label. A duplicate (calendar_id, name) is rejected by
-- uniq_calendar_labels_calendar_name.
INSERT INTO calendar_labels (
  public_id,
  workspace_id,
  calendar_id,
  name,
  color
) VALUES (?, ?, ?, ?, ?);

-- name: UpdateCalendarLabel :exec
-- Rename or recolour a label, addressed by internal id. The caller resolved
-- the label with FindCalendarLabelByPublicId under the calendar lock, so a
-- zero count (an unchanged name and colour) is not a missing row.
UPDATE calendar_labels
SET name = ?,
    color = ?
WHERE id = ?;

-- name: DeleteCalendarLabel :execrows
-- Remove a label. The links to it go with it through the foreign key
-- cascade. Zero rows means the label is not on this calendar.
DELETE FROM calendar_labels
WHERE id = ?
  AND calendar_id = ?;

-- name: GetEventLabel :one
-- The label an event row is filed under, by internal ids. sql.ErrNoRows
-- means the event carries none.
SELECT label_id
FROM calendar_event_labels
WHERE event_id = ?
LIMIT 1;

-- name: SetEventLabel :exec
-- File an event row under a label, replacing any label it already carries.
-- event_id is the primary key, so an event holds at most one link.
INSERT INTO calendar_event_labels (
  event_id,
  label_id
) VALUES (?, ?)
ON DUPLICATE KEY UPDATE
  label_id = VALUES(label_id);

-- name: DeleteEventLabel :execrows
-- Remove an event row's label link. Zero rows means the link was never there,
-- which is the state the caller wants.
DELETE FROM calendar_event_labels
WHERE event_id = ?;

-- name: ListEnabledOverrideIdsOfSeries :many
-- Internal ids of a series' live overrides, so a label set on the series can
-- be propagated to each of them.
SELECT id
FROM calendar_events
WHERE recurrence_parent_id = ?
  AND workspace_id = ?
  AND enabled = TRUE
ORDER BY id ASC
LIMIT 5000;
