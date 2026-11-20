-- name: ListInbox :many
-- List a workspace's inbox via v_inbox.
--
-- The signal is the row; the task it points at is a separate thing the
-- reader may or may not be allowed to see. So the item stays and the
-- task columns are blanked rather than the row being dropped: a signal
-- arriving is not the task's secret, its title is. Dropping the row
-- would also make the inbox silently lose items whose linkage the
-- reader cannot follow, which reads as data loss rather than privacy.
SELECT
  v.workspace_id,
  v.workspace_public_id,
  v.public_id,
  v.task_public_id,
  v.task_title,
  -- Whether the reader may follow the link to the task. Computed here
  -- rather than blanking the columns in place, because the columns are
  -- nullable for a different reason — a signal need not name a task at
  -- all — and collapsing the two would leave the mapper unable to tell
  -- "no task" from "not yours". The mapper applies the flag; see
  -- eventacl.AttendeeExistsSQL for the same shape on calendar events.
  (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR v.task_internal_id IS NULL
    OR v.task_visibility = 'public'
    OR (v.task_visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = v.task_project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (v.task_visibility = 'private' AND (
      v.task_created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = v.task_internal_id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  ) AS task_visible,
  v.source,
  v.kind,
  v.external_id,
  v.payload_json,
  v.received_at,
  v.updated_at,
  v.created_at
FROM v_inbox v
WHERE v.workspace_id = ?
ORDER BY v.received_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ListInboxForUser :many
-- List inbox items across every workspace the actor is an active member of.
SELECT
  v.workspace_id,
  v.workspace_public_id,
  v.public_id,
  v.task_public_id,
  v.task_title,
  -- Whether the reader may follow the link to the task. Computed here
  -- rather than blanking the columns in place, because the columns are
  -- nullable for a different reason — a signal need not name a task at
  -- all — and collapsing the two would leave the mapper unable to tell
  -- "no task" from "not yours". The mapper applies the flag; see
  -- eventacl.AttendeeExistsSQL for the same shape on calendar events.
  (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR v.task_internal_id IS NULL
    OR v.task_visibility = 'public'
    OR (v.task_visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = v.task_project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (v.task_visibility = 'private' AND (
      v.task_created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = v.task_internal_id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  ) AS task_visible,
  v.source,
  v.kind,
  v.external_id,
  v.payload_json,
  v.received_at,
  v.updated_at,
  v.created_at
FROM v_inbox v
INNER JOIN workspace_members wm
  ON wm.workspace_id = v.workspace_id
  AND wm.user_id = ?
  AND wm.enabled = TRUE
ORDER BY v.received_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: CountInbox :one
-- Count of a workspace's inbox, the total ListInbox pages through. It is
-- a bare count so the per-row visibility expression the list computes is
-- evaluated only for the rows a page returns.
SELECT COUNT(*) AS total
FROM v_inbox v
WHERE v.workspace_id = ?;

-- name: CountInboxForUser :one
-- Count of the actor's cross-workspace inbox, the total ListInboxForUser
-- pages through.
SELECT COUNT(*) AS total
FROM v_inbox v
INNER JOIN workspace_members wm
  ON wm.workspace_id = v.workspace_id
  AND wm.user_id = ?
  AND wm.enabled = TRUE;

-- name: GetInboxSignalForIntake :one
-- Load one active source signal inside the workspace whose membership the
-- caller already proved. Payload remains JSON data; source-specific display
-- fields are projected by the sourceintake package, never treated as HTML.
SELECT
  s.id,
  s.public_id,
  s.workspace_id,
  s.task_id,
  s.source,
  s.kind,
  s.payload_json,
  s.received_at
FROM signals s
WHERE s.workspace_id = ?
  AND s.public_id = ?
  AND s.enabled = TRUE
LIMIT 1
FOR UPDATE;

-- name: LockSignalForIntakeConversion :one
-- Lock the source association before locking its intake row and allocating
-- the conversion task. An archived signal still matches: archiving hides it
-- from the inbox, not from the intake it was captured into. task_id remains
-- independent of the original signal subject_type/subject_id pair.
SELECT
  s.id,
  s.public_id,
  s.workspace_id,
  s.task_id,
  s.source,
  s.kind
FROM signals s
WHERE s.workspace_id = ?
  AND s.id = ?
LIMIT 1
FOR UPDATE;

-- name: LinkInboxSignalToTask :execrows
-- Add a same-workspace task link without replacing an existing signal link.
-- Archived signals are linked too, matching LockSignalForIntakeConversion.
UPDATE signals
SET task_id = ?
WHERE workspace_id = ?
  AND id = ?
  AND task_id IS NULL;

-- name: GetSignalTaskForArchive :one
-- Internal id of the task a signal is attached to, through task_id or a
-- task subject. subject_id counts only when it joins a task in the same
-- workspace. ErrNoRows means the signal is not attached to a task.
SELECT t.id
FROM signals s
INNER JOIN tasks t
  ON t.workspace_id = s.workspace_id
 AND t.id = COALESCE(s.task_id, IF(s.subject_type = 'task', s.subject_id, NULL))
WHERE s.workspace_id = ?
  AND s.public_id = ?;

-- name: ArchiveInboxItem :execrows
-- Archive a signal by soft-disabling it. The inbox view excludes disabled rows.
UPDATE signals
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: SnoozeInboxItem :execrows
-- Snooze a signal by pushing its received_at forward. Minimal impl;
-- a dedicated snoozed_until_at column may be added later on.
UPDATE signals
SET received_at = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;
