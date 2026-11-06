-- A batch is readable only by its initiating, currently enabled workspace
-- member, while every captured task still exists and passes that member's
-- current task ACL. Workspace admins bypass only task-level visibility.
CREATE OR REPLACE VIEW v_ai_suggestion_readable_batches AS
SELECT
  b.id,
  b.public_id,
  b.workspace_id,
  b.user_id
FROM ai_suggestion_batches b
INNER JOIN workspaces w
  ON w.id = b.workspace_id AND w.enabled = TRUE
INNER JOIN workspace_members wm
  ON wm.workspace_id = b.workspace_id
 AND wm.user_id = b.user_id
 AND wm.enabled = TRUE
INNER JOIN users u
  ON u.id = b.user_id AND u.enabled = TRUE
WHERE b.enabled = TRUE
  AND b.provenance_valid = TRUE
  AND b.user_id IS NOT NULL
  -- Correlated, not a grouped view: a GROUP BY view cannot merge and is
  -- materialized over every batch in the instance before b is filtered.
  AND b.input_count = (
    SELECT COUNT(*)
    FROM ai_suggestion_batches_inputs i
    WHERE i.batch_id = b.id
  )
  AND NOT EXISTS (
    SELECT 1
    FROM ai_suggestion_batches_inputs bi
    LEFT JOIN v_task_list_all task
      ON task.workspace_id = b.workspace_id
     AND task.public_id = bi.task_public_id
    WHERE bi.batch_id = b.id
      AND (
        task.task_internal_id IS NULL
        OR (
          wm.role NOT IN ('owner', 'admin')
          AND NOT COALESCE((
            task.visibility = 'public'
            OR (task.visibility = 'project' AND EXISTS (
              SELECT 1 FROM project_members pm_vis
              WHERE pm_vis.project_id = task.project_id
                AND pm_vis.user_id = wm.user_id
                AND pm_vis.enabled = TRUE
            ))
            OR (task.visibility = 'private' AND (
              task.created_by_user_id = wm.user_id
              OR EXISTS (
                SELECT 1 FROM task_actors ta_vis
                WHERE ta_vis.task_id = task.task_internal_id
                  AND ta_vis.kind = 'user'
                  AND ta_vis.user_id = wm.user_id
                  AND ta_vis.enabled = TRUE
              )
            ))
          ), FALSE)
        )
      )
  );
