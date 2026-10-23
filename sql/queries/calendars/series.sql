-- ====================================
-- Series transform
--
-- The statements calendaroccurrence's series transform issues inside its
-- own transaction while it splits, ends or realigns a series. None of them
-- serves a viewer: they decide which rows still name an occurrence the
-- series produces, which does not depend on who is asking, so they carry
-- no visibility filter and stay out of events.sql, whose list queries do.
-- ====================================

-- name: LockCalendarEventSeries :one
-- The series columns of one master, read under a row lock.
--
-- A write that derives a new exception list, a truncation or a continuing
-- series from these columns has to read them inside its own transaction
-- and hold them: built from a read taken before it, two concurrent edits
-- each write a list missing the other's entry, and a cancelled occurrence
-- comes back.
SELECT
  ce.id,
  COALESCE(ce.recurrence_rule, CAST('null' AS JSON)) AS recurrence_rule,
  ce.recurrence_end,
  COALESCE(ce.recurrence_exceptions, CAST('null' AS JSON)) AS recurrence_exceptions
FROM calendar_events ce
WHERE ce.id = ?
  AND ce.workspace_id = ?
  AND ce.enabled = TRUE
FOR UPDATE;

-- name: SetCalendarEventSeriesExceptions :exec
-- Write one master's exception list, keyed by the internal id the series
-- lock read it under.
--
-- An override row deleted on its own names its master only by that id, and
-- the cancellation it leaves behind belongs on the master: without it the
-- master produces the occurrence again at its original slot.
UPDATE calendar_events
SET recurrence_exceptions = ?
WHERE id = ?
  AND workspace_id = ?
  AND enabled = TRUE;

-- name: ListCalendarEventOverridesFromStart :many
-- Every override of one master naming an occurrence at or after a point,
-- soft-deleted ones included, for a write that moves or withdraws them as
-- the series they name changes shape.
--
-- Not viewer-scoped: this read decides whether a row still names an
-- occurrence its series produces, which does not depend on who is asking.
-- Disabled rows are listed because the unique key on (recurrence_parent_id,
-- recurrence_original_start) counts them, so a move has to know where they
-- sit.
--
-- The LIMIT is the runaway bound ListCalendarEventOverriddenStarts uses.
SELECT
  ce.id,
  ce.public_id,
  ce.calendar_id,
  ce.recurrence_original_start,
  ce.enabled
FROM calendar_events ce
WHERE ce.workspace_id = ?
  AND ce.recurrence_parent_id = sqlc.arg('parent_id')
  AND ce.recurrence_original_start >= sqlc.arg('from_start')
ORDER BY ce.recurrence_original_start ASC
LIMIT 5000;

-- name: RebaseCalendarEventOverride :exec
-- Point an override at the occurrence it stands for after its series moved:
-- the same position in the rule, at the instant the rule now gives it.
--
-- recurrence_parent_id IS NOT NULL confines the statement to override rows.
UPDATE calendar_events
SET recurrence_original_start = ?
WHERE id = ?
  AND workspace_id = ?
  AND recurrence_parent_id IS NOT NULL;

-- name: ListCalendarEventReminderClaims :many
-- The occurrences of one event whose reminder has already been sent, from
-- a point onwards.
--
-- Read when a split or an override hands occurrences to a new row id: the
-- claim is keyed by (event_id, occurrence_start), so without carrying it
-- across the new row's occurrence looks unreminded and is sent again.
SELECT occurrence_start
FROM calendar_event_reminders
WHERE workspace_id = ?
  AND event_id = ?
  AND occurrence_start >= sqlc.arg('from_start')
ORDER BY occurrence_start ASC
LIMIT 1000;

-- name: CopyCalendarEventReminderClaim :exec
-- Record a claim already taken for an occurrence under the row that now
-- carries it. IGNORE because the claim may already be there, which is the
-- state this exists to reach.
INSERT IGNORE INTO calendar_event_reminders (workspace_id, event_id, occurrence_start)
VALUES (?, ?, ?);
