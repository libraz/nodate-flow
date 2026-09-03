-- name: InsertSignal :execlastid
-- Insert an inbound signal (manual or webhook).
-- Dedup is workspace-scoped via UNIQUE (workspace_id, source, external_id)
-- when external_id is non-NULL. Duplicate deliveries are silently ignored
-- via INSERT IGNORE; LastInsertId() returns 0 when the row was a duplicate.
-- subject_type is NOT NULL per sql/flow/tables/signals.sql (ADR 0008 D1) so every
-- caller must resolve a kind-appropriate subject; subject_id stays NULL for
-- workspace-scoped signals (workspace_id already identifies the owner).
-- judge_run_id / judge_output_json / confidence / applied_at stay NULL at
-- insert time; the judge fills them later. expires_at is provider-derived
-- TTL (presence transitions, weather window) and is NULL for signals that
-- never expire (manual).
INSERT IGNORE INTO signals (
  public_id,
  workspace_id,
  task_id,
  source,
  kind,
  external_id,
  payload_json,
  received_at,
  subject_type,
  subject_id,
  expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListSignalsForTask :many
-- List signals attached to a task via v_inbox.
SELECT
  v.public_id,
  v.task_public_id,
  v.task_title,
  v.source,
  v.kind,
  v.external_id,
  v.payload_json,
  v.received_at,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_inbox v
WHERE v.workspace_id = ?
  AND v.task_public_id = ?
  -- The task title rides on every row, so the signals are listable only
  -- when the actor may see the task they hang off. Elevated roles skip
  -- the check.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
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
ORDER BY v.received_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ListUnattachedSignals :many
-- List signals in a workspace that have no task linkage yet.
SELECT
  v.public_id,
  v.source,
  v.kind,
  v.external_id,
  v.payload_json,
  v.received_at,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_inbox v
WHERE v.workspace_id = ?
  AND v.task_public_id IS NULL
ORDER BY v.received_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: AttachSignalToTask :execrows
-- Link an existing signal to a task by public_id.
UPDATE signals s
SET s.task_id = (
  SELECT t.id FROM tasks t
  WHERE t.workspace_id = ? AND t.public_id = ? AND t.enabled = TRUE
)
WHERE s.workspace_id = ?
  AND s.public_id = ?
  AND s.enabled = TRUE;
