-- v_task_list_all
-- Base projection that includes BOTH active and archived tasks.
-- Acts as the single source of column definitions for the task list family.
-- Consumers should prefer v_task_list (active) or v_task_list_archived
-- (archived) which filter this base view by archived_at. MySQL 8.4 expands
-- single-level views via the MERGE optimizer, so there is no runtime cost.
--
-- The three child-table columns are correlated scalar subqueries rather
-- than joined derived tables, and that is a tenancy property, not a
-- micro-optimisation. A derived table cannot see the outer workspace
-- predicate, so MySQL has to materialise it over every row in the
-- instance before the join can filter: reading one 200-task project meant
-- aggregating every task_actors and task_labels row of every tenant
-- first, and one large tenant slowed the list down for all the others.
-- Correlating on t.id instead evaluates each subquery per emitted row
-- against the child tables' task_id indexes, so the work scales with the
-- page, not with the instance. Adding a fourth child column belongs in
-- the same shape; a LEFT JOIN over a GROUP BY reintroduces the problem.
--
-- Correlating on t.id also carries the enabled check that the previous
-- shape spelled out: the subqueries only ever see the outer row, which
-- the final WHERE already restricts to enabled tasks, so a child row of
-- a soft-disabled task cannot surface here.
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
  -- The first assignee in display order, and NULL when that actor is an
  -- AI agent or a disabled user -- the ORDER BY picks the row, the LEFT
  -- JOIN decides whether it has a name to show. Resolving to the *next*
  -- assignee instead would silently promote someone the workspace did
  -- not put first.
  (SELECT u.public_id
     FROM task_actors ta
     LEFT JOIN users u
       ON u.id = ta.user_id AND u.enabled = TRUE
    WHERE ta.task_id = t.id
      AND ta.enabled = TRUE
      AND ta.role = 'assignee'
    ORDER BY ta.sort_weight ASC, ta.id ASC
    LIMIT 1) AS primary_assignee_public_id,
  -- Counts human assignees only; the INNER JOIN drops agent actors
  -- (user_id IS NULL) and actors whose user has been disabled.
  (SELECT COUNT(*)
     FROM task_actors ta
     INNER JOIN users u
       ON u.id = ta.user_id AND u.enabled = TRUE
    WHERE ta.task_id = t.id
      AND ta.enabled = TRUE
      AND ta.role = 'assignee') AS assignee_count,
  -- label_ids is a comma-separated list of UUID *text*, not of raw
  -- BINARY(16). Concatenating the binary form is unrecoverable in both
  -- directions: 0x2C occurs inside legitimate UUIDv7 bytes so the reader
  -- cannot tell a separator from payload, and the bytes are not valid
  -- UTF-8 so JSON encoding replaces them with U+FFFD before the reader
  -- ever sees them. BIN_TO_UUID uses swap_flag 0 to match the
  -- UUID_TO_BIN(?, 0) form every writer uses.
  --
  -- The SUBSTRING_INDEX cap is what keeps GROUP_CONCAT honest. Its
  -- result is clipped at group_concat_max_len (1024 bytes by default)
  -- with no error and no marker, which at 37 bytes per entry would cut
  -- the 28th UUID in half and hand the reader a malformed id. Cutting at
  -- the 20th entry lands on byte 739, so the clip can only ever damage
  -- text this expression has already discarded: the column is either the
  -- whole list or its first 20 entries in display order -- never a
  -- half-written one. The list-row badge strip is the only consumer.
  -- Callers needing every label of a task read the task's label
  -- collection directly.
  -- The COALESCE sits inside SUBSTRING_INDEX, not around the subquery:
  -- a task with no labels aggregates to NULL, and the empty string is
  -- what "no labels" already meant to every consumer. Wrapping the
  -- subquery from the outside instead leaves the generated reader with
  -- no column type to work from and it falls back to interface{}.
  (SELECT SUBSTRING_INDEX(
            COALESCE(GROUP_CONCAT(BIN_TO_UUID(l.public_id, 0)
              ORDER BY tl.sort_weight ASC, tl.id ASC SEPARATOR ','), ''),
            ',', 20)
     FROM task_labels tl
     INNER JOIN labels l
       ON l.id = tl.label_id AND l.enabled = TRUE
    WHERE tl.task_id = t.id
      AND tl.enabled = TRUE) AS label_ids
FROM tasks t
INNER JOIN projects p
  ON p.id = t.project_id AND p.enabled = TRUE
INNER JOIN workspaces w
  ON w.id = t.workspace_id AND w.enabled = TRUE
LEFT JOIN tasks pt
  ON pt.id = t.parent_task_id AND pt.enabled = TRUE
WHERE t.enabled = TRUE;
