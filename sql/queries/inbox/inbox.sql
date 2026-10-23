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

-- name: GetInboxItemTaskVisible :one
-- Resolve one inbox item in a workspace and whether the reader may read
-- the task it points at. No row means the id names no inbox item here,
-- which is what an AI suggestion reaction has to refuse.
SELECT
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
  ) AS task_visible
FROM v_inbox v
WHERE v.workspace_id = sqlc.arg('workspace_id')
  AND v.public_id = sqlc.arg('public_id')
LIMIT 1;

-- name: ListPendingAiSuggestionsForReader :many
-- Pending AI triage suggestions a reader may see. A suggestion is pending
-- while no later applied / dismissed event names the same inbox item; it
-- is the reader's only while that item is still in the inbox and the task
-- it points at is one they may read, because the stored reasoning is the
-- model talking about that task.
SELECT
  e.public_id,
  e.occurred_at,
  e.payload_json
FROM events e
INNER JOIN v_inbox v
  ON v.workspace_id = e.workspace_id
 AND v.public_id = CASE
       WHEN IS_UUID(JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.inbox_item_id')))
       THEN UUID_TO_BIN(JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.inbox_item_id')), 0)
     END
WHERE e.workspace_id = sqlc.arg('workspace_id')
  AND e.type = 'ai.suggestion.proposed'
  AND NOT EXISTS (
    SELECT 1 FROM events e2
    WHERE e2.workspace_id = e.workspace_id
      AND e2.type IN ('ai.suggestion.applied', 'ai.suggestion.dismissed')
      AND e2.id > e.id
      AND JSON_EXTRACT(e2.payload_json, '$.inbox_item_id') = JSON_EXTRACT(e.payload_json, '$.inbox_item_id')
  )
  AND (
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
  )
ORDER BY e.occurred_at DESC
LIMIT 100;
