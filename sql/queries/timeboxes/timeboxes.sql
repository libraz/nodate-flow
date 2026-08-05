-- name: CreateTimebox :execlastid
-- Insert a new timebox (sprint / iteration / cycle).
INSERT INTO timeboxes (
  public_id,
  workspace_id,
  project_id,
  creator_id,
  name,
  description,
  starts_on,
  ends_on
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListTimeboxesForWorkspace :many
-- List enabled timeboxes for a workspace with creator info and pagination.
SELECT
  tb.public_id,
  tb.workspace_id,
  p.public_id AS project_public_id,
  p.name AS project_name,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  tb.name,
  tb.description,
  tb.starts_on,
  tb.ends_on,
  tb.status,
  tb.updated_at,
  tb.created_at,
  COUNT(*) OVER() AS total
FROM timeboxes tb
INNER JOIN users u ON u.id = tb.creator_id
LEFT JOIN projects p ON p.id = tb.project_id
WHERE tb.workspace_id = ?
  AND tb.enabled = TRUE
ORDER BY tb.starts_on DESC, tb.id DESC
LIMIT ? OFFSET ?;

-- name: GetTimeboxByPublicId :one
-- Fetch a single timebox by workspace_id + public_id.
SELECT
  tb.id,
  tb.public_id,
  tb.workspace_id,
  p.public_id AS project_public_id,
  p.name AS project_name,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  tb.name,
  tb.description,
  tb.starts_on,
  tb.ends_on,
  tb.status,
  tb.updated_at,
  tb.created_at
FROM timeboxes tb
INNER JOIN users u ON u.id = tb.creator_id
LEFT JOIN projects p ON p.id = tb.project_id
WHERE tb.workspace_id = ?
  AND tb.public_id = ?
  AND tb.enabled = TRUE
LIMIT 1;

-- name: UpdateTimebox :exec
-- Update mutable timebox fields.
UPDATE timeboxes
SET name = ?,
    description = ?,
    starts_on = ?,
    ends_on = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: UpdateTimeboxStatus :exec
-- Transition timebox lifecycle status.
UPDATE timeboxes
SET status = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: DisableTimebox :exec
-- Soft-delete a timebox.
UPDATE timeboxes
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;

-- name: AddTaskToTimebox :exec
-- Associate a task with a timebox. Caller must resolve IDs beforehand.
INSERT INTO timebox_tasks (public_id, workspace_id, timebox_id, task_id)
VALUES (?, ?, ?, ?);

-- name: RemoveTaskFromTimebox :exec
-- Remove a task from a timebox.
DELETE FROM timebox_tasks
WHERE timebox_id = ?
  AND task_id = ?;

-- name: ListTasksForTimebox :many
-- List tasks belonging to a timebox with pagination.
SELECT
  t.public_id,
  t.title,
  t.derived_state,
  t.priority,
  t.due_on,
  t.started_on,
  t.sort_weight,
  t.updated_at,
  t.created_at,
  COUNT(*) OVER() AS total
FROM timebox_tasks tt
INNER JOIN tasks t ON t.id = tt.task_id AND t.enabled = TRUE
WHERE tt.timebox_id = sqlc.arg('timebox_id')
  AND tt.enabled = TRUE
  -- A timebox may hold tasks the reader is not allowed to see; the
  -- membership row grants nothing about the task's own visibility.
  -- The same predicate gates the progress counts, so the two numbers
  -- describe the same set the caller is shown.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR
    (
      t.visibility = 'public'
      OR (t.visibility = 'project' AND EXISTS (
        SELECT 1 FROM project_members pm_vis
        WHERE pm_vis.project_id = t.project_id
          AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND pm_vis.enabled = TRUE
      ))
      OR (t.visibility = 'private' AND (
        t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        OR EXISTS (
          SELECT 1 FROM task_actors ta_vis
          WHERE ta_vis.task_id = t.id
            AND ta_vis.kind = 'user'
            AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            AND ta_vis.enabled = TRUE
        )
      ))
    )
  )
ORDER BY t.priority DESC, t.created_at ASC, t.id ASC
LIMIT ? OFFSET ?;

-- name: CountTasksForTimebox :one
-- Count total and completed tasks in a timebox for progress tracking.
SELECT
  COUNT(*) AS total_tasks,
  SUM(CASE WHEN t.derived_state IN ('done', 'cancelled') THEN 1 ELSE 0 END) AS completed_tasks
FROM timebox_tasks tt
INNER JOIN tasks t ON t.id = tt.task_id AND t.enabled = TRUE
WHERE tt.timebox_id = sqlc.arg('timebox_id')
  AND tt.enabled = TRUE
  -- A timebox may hold tasks the reader is not allowed to see; the
  -- membership row grants nothing about the task's own visibility.
  -- The same predicate gates the progress counts, so the two numbers
  -- describe the same set the caller is shown.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR
    (
      t.visibility = 'public'
      OR (t.visibility = 'project' AND EXISTS (
        SELECT 1 FROM project_members pm_vis
        WHERE pm_vis.project_id = t.project_id
          AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND pm_vis.enabled = TRUE
      ))
      OR (t.visibility = 'private' AND (
        t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        OR EXISTS (
          SELECT 1 FROM task_actors ta_vis
          WHERE ta_vis.task_id = t.id
            AND ta_vis.kind = 'user'
            AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            AND ta_vis.enabled = TRUE
        )
      ))
    )
  );
