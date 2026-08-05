-- name: ExportTasksForWorkspace :many
-- Fetch tasks for CSV/JSON export across an entire workspace.
-- Includes project name and primary assignee for human-readable output.
SELECT
  t.public_id,
  t.title,
  t.description,
  t.derived_state,
  t.priority,
  t.due_on,
  t.started_on,
  t.completed_at,
  p.public_id AS project_public_id,
  p.name AS project_name,
  a_user.public_id AS assignee_public_id,
  a_user.display_name AS assignee_display_name,
  t.updated_at,
  t.created_at
FROM tasks t
INNER JOIN projects p ON p.id = t.project_id AND p.enabled = TRUE
-- One row per task, whatever the number of assignees. Joining
-- task_actors on task_id alone multiplies the task across its
-- assignees, and an export is both counted and capped by row: a task
-- with three assignees appears three times, inflates the total, and
-- pushes three real tasks past the limit, where they are dropped
-- without a word. Matching a single actor row keeps one task one row.
--
-- The primary assignee is picked by the same order the task list uses
-- (_v_task_list_all), so the two surfaces name the same person. That
-- view ranks with ROW_NUMBER() because it also counts assignees; here
-- the equivalent single-row match is written as a subquery so a_user
-- stays a plain column reference and the generated types keep the
-- nullability a LEFT JOIN implies.
LEFT JOIN task_actors ta ON ta.id = (
  SELECT ta_pick.id
  FROM task_actors ta_pick
  WHERE ta_pick.task_id = t.id
    AND ta_pick.role = 'assignee'
    AND ta_pick.kind = 'user'
    AND ta_pick.enabled = TRUE
  ORDER BY ta_pick.sort_weight ASC, ta_pick.id ASC
  LIMIT 1
)
LEFT JOIN users a_user ON a_user.id = ta.user_id AND a_user.enabled = TRUE
WHERE t.workspace_id = sqlc.arg('workspace_id')
  AND t.enabled = TRUE
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR t.visibility = 'public'
    OR (
      t.visibility = 'project'
      AND EXISTS (
        SELECT 1
        FROM project_members pm
        WHERE pm.project_id = t.project_id
          AND pm.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND pm.enabled = TRUE
      )
    )
    OR (
      t.visibility = 'private'
      AND (
        t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        OR EXISTS (
          SELECT 1
          FROM task_actors ta_vis
          WHERE ta_vis.task_id = t.id
            AND ta_vis.kind = 'user'
            AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            AND ta_vis.enabled = TRUE
        )
      )
    )
  )
ORDER BY t.created_at DESC, t.id DESC
LIMIT ?;

-- name: ExportTasksForLens :many
-- Fetch tasks for export scoped to a workspace + project (lens-scoped).
-- Lens filter/sort logic is applied in Go code; this provides the data set.
SELECT
  t.public_id,
  t.title,
  t.description,
  t.derived_state,
  t.priority,
  t.due_on,
  t.started_on,
  t.completed_at,
  p.public_id AS project_public_id,
  p.name AS project_name,
  a_user.public_id AS assignee_public_id,
  a_user.display_name AS assignee_display_name,
  t.updated_at,
  t.created_at
FROM tasks t
INNER JOIN projects p ON p.id = t.project_id AND p.enabled = TRUE
-- One row per task, whatever the number of assignees. Joining
-- task_actors on task_id alone multiplies the task across its
-- assignees, and an export is both counted and capped by row: a task
-- with three assignees appears three times, inflates the total, and
-- pushes three real tasks past the limit, where they are dropped
-- without a word. Matching a single actor row keeps one task one row.
--
-- The primary assignee is picked by the same order the task list uses
-- (_v_task_list_all), so the two surfaces name the same person. That
-- view ranks with ROW_NUMBER() because it also counts assignees; here
-- the equivalent single-row match is written as a subquery so a_user
-- stays a plain column reference and the generated types keep the
-- nullability a LEFT JOIN implies.
LEFT JOIN task_actors ta ON ta.id = (
  SELECT ta_pick.id
  FROM task_actors ta_pick
  WHERE ta_pick.task_id = t.id
    AND ta_pick.role = 'assignee'
    AND ta_pick.kind = 'user'
    AND ta_pick.enabled = TRUE
  ORDER BY ta_pick.sort_weight ASC, ta_pick.id ASC
  LIMIT 1
)
LEFT JOIN users a_user ON a_user.id = ta.user_id AND a_user.enabled = TRUE
WHERE t.workspace_id = sqlc.arg('workspace_id')
  AND t.project_id = sqlc.arg('project_id')
  AND t.enabled = TRUE
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR t.visibility = 'public'
    OR (
      t.visibility = 'project'
      AND EXISTS (
        SELECT 1
        FROM project_members pm
        WHERE pm.project_id = t.project_id
          AND pm.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND pm.enabled = TRUE
      )
    )
    OR (
      t.visibility = 'private'
      AND (
        t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        OR EXISTS (
          SELECT 1
          FROM task_actors ta_vis
          WHERE ta_vis.task_id = t.id
            AND ta_vis.kind = 'user'
            AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            AND ta_vis.enabled = TRUE
        )
      )
    )
  )
ORDER BY t.created_at DESC, t.id DESC
LIMIT ?;
