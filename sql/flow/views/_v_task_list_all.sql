-- v_task_list_all
-- Base projection that includes BOTH active and archived tasks.
-- Acts as the single source of column definitions for the task list family.
-- Consumers should prefer v_task_list (active) or v_task_list_archived
-- (archived) which filter this base view by archived_at. MySQL 8.4 expands
-- single-level views via the MERGE optimizer, so there is no runtime cost.
--
-- Defense-in-depth: every aggregate subquery over a task_* child table
-- INNER JOINs the parent tasks row with `enabled = TRUE` even though the
-- outer SELECT already filters disabled tasks. This prevents accidental
-- leakage of child rows belonging to soft-disabled tasks if these
-- subqueries are ever lifted into a different consumer.
CREATE OR REPLACE ALGORITHM=MERGE VIEW v_task_list_all AS
SELECT
  t.workspace_id,
  t.project_id,
  t.created_by_user_id,
  t.public_id,
  p.public_id AS project_public_id,
  p.name AS project_name,
  p.identifier AS project_identifier,
  t.task_number,
  pt.public_id AS parent_task_public_id,
  t.title,
  t.visibility,
  t.derived_state,
  t.priority,
  t.due_on,
  t.started_on,
  t.completed_at,
  t.archived_at,
  t.sort_weight,
  t.updated_at,
  t.created_at,
  assignees.primary_assignee_public_id,
  COALESCE(assignees.assignee_count, 0) AS assignee_count,
  labels.label_ids
FROM tasks t
INNER JOIN projects p
  ON p.id = t.project_id AND p.enabled = TRUE
INNER JOIN workspaces w
  ON w.id = t.workspace_id AND w.enabled = TRUE
LEFT JOIN tasks pt
  ON pt.id = t.parent_task_id AND pt.enabled = TRUE
LEFT JOIN (
  SELECT
    ta.task_id,
    MIN(CASE WHEN rn = 1 THEN u.public_id END) AS primary_assignee_public_id,
    COUNT(*) AS assignee_count
  FROM (
    SELECT ta2.task_id, ta2.user_id,
           ROW_NUMBER() OVER (PARTITION BY ta2.task_id ORDER BY ta2.sort_weight ASC, ta2.id ASC) AS rn
    FROM task_actors ta2
    INNER JOIN tasks ta2t ON ta2t.id = ta2.task_id AND ta2t.enabled = TRUE
    WHERE ta2.enabled = TRUE AND ta2.role = 'assignee'
  ) ta
  INNER JOIN users u ON u.id = ta.user_id AND u.enabled = TRUE
  GROUP BY ta.task_id
) assignees ON assignees.task_id = t.id
LEFT JOIN (
  -- label_ids is a comma-separated list of UUID *text*, not of raw
  -- BINARY(16). Concatenating the binary form is unrecoverable in both
  -- directions: 0x2C occurs inside legitimate UUIDv7 bytes so the reader
  -- cannot tell a separator from payload, and the bytes are not valid
  -- UTF-8 so JSON encoding replaces them with U+FFFD before the reader
  -- ever sees them. BIN_TO_UUID uses swap_flag 0 to match the
  -- UUID_TO_BIN(?, 0) form every writer uses.
  --
  -- The rn cap is what keeps GROUP_CONCAT honest. Its result is clipped
  -- at group_concat_max_len (1024 bytes by default) with no error and no
  -- marker, which at 37 bytes per entry would cut the 28th UUID in half
  -- and hand the reader a malformed id. Capping the aggregate below that
  -- point makes the clip unreachable, so the column is either the whole
  -- list or its first 20 entries in display order -- never a
  -- half-written one. The list-row badge strip is the only consumer.
  -- Callers needing every label of a task read the task's label
  -- collection directly.
  SELECT
    ranked.task_id,
    GROUP_CONCAT(BIN_TO_UUID(ranked.public_id, 0) ORDER BY ranked.rn ASC SEPARATOR ',') AS label_ids
  FROM (
    SELECT
      tl.task_id,
      l.public_id,
      ROW_NUMBER() OVER (PARTITION BY tl.task_id ORDER BY tl.sort_weight ASC, tl.id ASC) AS rn
    FROM task_labels tl
    INNER JOIN tasks tlt ON tlt.id = tl.task_id AND tlt.enabled = TRUE
    INNER JOIN labels l ON l.id = tl.label_id AND l.enabled = TRUE
    WHERE tl.enabled = TRUE
  ) ranked
  WHERE ranked.rn <= 20
  GROUP BY ranked.task_id
) labels ON labels.task_id = t.id
WHERE t.enabled = TRUE;
