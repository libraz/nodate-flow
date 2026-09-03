-- name: AddDependency :execlastid
-- Add a directed dependency between two tasks.
INSERT INTO task_dependencies (
  public_id,
  workspace_id,
  from_task_id,
  to_task_id,
  kind
) VALUES (?, ?, ?, ?, ?);

-- name: ListDependenciesForTask :many
-- List outgoing dependencies of a task. Returns the target task public_id.
SELECT
  td.public_id,
  td.kind,
  ft.public_id AS from_task_public_id,
  tt.public_id AS to_task_public_id,
  tt.title    AS to_task_title,
  tt.derived_state AS to_task_derived_state,
  td.updated_at,
  td.created_at,
  COUNT(*) OVER() AS total
FROM task_dependencies td
INNER JOIN tasks ft ON ft.id = td.from_task_id AND ft.enabled = TRUE
INNER JOIN tasks tt ON tt.id = td.to_task_id   AND tt.enabled = TRUE
WHERE td.workspace_id = sqlc.arg('workspace_id')
  AND ft.public_id = sqlc.arg('from_task_public_id')
  AND td.enabled = TRUE
  -- Reaching this route means the actor may see the task named in the
  -- path; the edge's far end is a different task and carries its own
  -- title, so it is filtered on its own visibility. Elevated roles skip
  -- the check.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR tt.visibility = 'public'
    OR (tt.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_tt
      WHERE pm_tt.project_id = tt.project_id
        AND pm_tt.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_tt.enabled = TRUE
    ))
    OR (tt.visibility = 'private' AND (
      tt.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_tt
        WHERE ta_tt.task_id = tt.id
          AND ta_tt.kind = 'user'
          AND ta_tt.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_tt.enabled = TRUE
      )
    ))
  )
ORDER BY td.created_at ASC, td.public_id ASC
LIMIT ? OFFSET ?;

-- name: ListIncomingDependenciesForTask :many
-- List incoming dependencies of a task (edges that point AT this task).
SELECT
  td.public_id,
  td.kind,
  ft.public_id AS from_task_public_id,
  ft.title    AS from_task_title,
  ft.derived_state AS from_task_derived_state,
  tt.public_id AS to_task_public_id,
  td.updated_at,
  td.created_at,
  COUNT(*) OVER() AS total
FROM task_dependencies td
INNER JOIN tasks ft ON ft.id = td.from_task_id AND ft.enabled = TRUE
INNER JOIN tasks tt ON tt.id = td.to_task_id   AND tt.enabled = TRUE
WHERE td.workspace_id = sqlc.arg('workspace_id')
  AND tt.public_id = sqlc.arg('to_task_public_id')
  AND td.enabled = TRUE
  -- Reaching this route means the actor may see the task named in the
  -- path; the edge's far end is a different task and carries its own
  -- title, so it is filtered on its own visibility. Elevated roles skip
  -- the check.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR ft.visibility = 'public'
    OR (ft.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_ft
      WHERE pm_ft.project_id = ft.project_id
        AND pm_ft.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_ft.enabled = TRUE
    ))
    OR (ft.visibility = 'private' AND (
      ft.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_ft
        WHERE ta_ft.task_id = ft.id
          AND ta_ft.kind = 'user'
          AND ta_ft.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_ft.enabled = TRUE
      )
    ))
  )
ORDER BY td.created_at ASC, td.public_id ASC
LIMIT ? OFFSET ?;

-- name: ListDependenciesForProject :many
-- List every task_dependencies edge where BOTH endpoints belong to the
-- given project. Used by the Gantt chart (to draw dependency arrows)
-- and by the List / Board views (to compute "blocked by open" badges).
SELECT
  td.public_id,
  td.kind,
  ft.public_id AS from_task_public_id,
  ft.derived_state AS from_task_derived_state,
  tt.public_id AS to_task_public_id,
  tt.derived_state AS to_task_derived_state
FROM task_dependencies td
INNER JOIN tasks ft ON ft.id = td.from_task_id AND ft.enabled = TRUE
INNER JOIN tasks tt ON tt.id = td.to_task_id   AND tt.enabled = TRUE
INNER JOIN projects p ON p.id = ft.project_id AND p.enabled = TRUE
WHERE td.workspace_id = ?
  AND p.public_id = ?
  AND ft.project_id = tt.project_id
  AND td.enabled = TRUE
ORDER BY td.created_at ASC, td.public_id ASC
LIMIT 5000;

-- name: ListDependencyEdgesForWorkspace :many
-- List every enabled dependency edge in the workspace as (from, to) internal
-- id pairs. Read-only callers (the "would this be rejected" preview) use this
-- one. Writers must use ListDependencyEdgesForWorkspaceForUpdate instead.
-- Internal ids are safe here because the result never leaves the handler
-- (json:"-" on *.id).
SELECT
  td.from_task_id,
  td.to_task_id
FROM task_dependencies td
WHERE td.workspace_id = ?
  AND td.enabled = TRUE;

-- name: ListDependencyEdgesForWorkspaceForUpdate :many
-- The same edge set, read under a lock. This is the serialization point for
-- adding an edge: the writer walks the graph to reject an edge that would
-- close a cycle, and the walk is only sound if no other writer can add an
-- edge between the walk and the insert.
--
-- The lock is workspace-wide because the read is. An earlier version locked
-- only the two endpoint projects, which serializes writers that share a
-- project but not a cycle drawn across four of them: two transactions
-- holding disjoint project pairs both walked an edge set missing the
-- other's edge, both passed, and both committed. The result is a cycle no
-- request could see, and it is silent -- `dependency.all_done` is evaluated
-- by a cycle-tolerant walk, so the constraint simply never becomes
-- satisfiable and every screen still looks healthy.
--
-- Locking the `workspaces` row instead (the obvious spelling of
-- "workspace-wide") is not an option: an exclusive lock on that row blocks
-- every INSERT into every table with a foreign key to workspaces, because
-- InnoDB takes a shared lock on the parent row for each such insert. That
-- includes the events log, so it would stall unrelated writes across the
-- whole workspace for the duration of the transaction. Locking the edge
-- rows themselves has no such reach: nothing references task_dependencies.
--
-- Range scanning (workspace_id, from_task_id) also gap-locks the workspace's
-- slice of that index, so a concurrent INSERT of a new edge blocks rather
-- than slipping in behind the walk. Writers scan in the same index order, so
-- they queue rather than deadlock; the one case that can deadlock is two
-- writers meeting on an empty range, where the gap locks are mutually
-- compatible but the insert intentions are not. That resolves to a retry
-- (dbretry.InTx), and a workspace with no edges has no cycle to close.
SELECT
  td.from_task_id,
  td.to_task_id
FROM task_dependencies td
WHERE td.workspace_id = ?
  AND td.enabled = TRUE
FOR UPDATE;

-- name: DeleteDependency :execrows
-- Soft-delete a dependency edge. Scoped by the owning from_task_id so a
-- sibling task's edge cannot be deleted through another task's path; the
-- affected-row count lets the handler return NOT_FOUND on a no-op.
UPDATE task_dependencies
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?
  AND from_task_id = ?
  AND enabled = TRUE;

-- name: ListRetroDraftsForWorkspace :many
-- List draft retrospective tasks: tasks linked back to a source task
-- via a task_dependencies row with kind='retro_of'. Backs the
-- retro draft queue endpoint (GET /workspaces/{wsId}/tasks/drafts?reason=retro).
-- The retro task itself is the from_task; to_task is the original task whose
-- lifecycle prompted the retrospective. Ordered newest-first so the queue
-- surfaces the freshest drafts at the top.
-- The internal task_id is returned so the handler can resolve the optional
-- created_by_agent attribution via FindRetroDraftAgent without re-issuing
-- a public_id -> id lookup per row (the json:"-" tag on *.id keeps it
-- out of the wire response).
SELECT
  t.id             AS task_id,
  t.public_id      AS task_public_id,
  t.title,
  t.description,
  t.created_at,
  src.public_id    AS source_task_public_id,
  src.title        AS source_task_title,
  COUNT(*) OVER()  AS total
FROM tasks t
INNER JOIN task_dependencies td
  ON td.from_task_id = t.id
 AND td.kind = 'retro_of'
 AND td.enabled = TRUE
INNER JOIN tasks src
  ON src.id = td.to_task_id
WHERE t.workspace_id = sqlc.arg('workspace_id')
  AND t.enabled = TRUE
  -- Archived drafts must not surface in the queue: Discard archives the
  -- task (POST /tasks/{id}/archive) and the UI expects the row to drop
  -- out immediately. Without this filter, archive_at flips to non-null
  -- but the row would still be returned by the next refetch — the
  -- optimistic UI removal would visually rubber-band back.
  AND t.archived_at IS NULL
  -- The draft's title and the source task's title are both on the wire,
  -- and the route is open to every workspace member, so a draft is
  -- listable only when the actor may see both ends. Elevated roles skip
  -- the check.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR (
      (
        t.visibility = 'public'
        OR (t.visibility = 'project' AND EXISTS (
          SELECT 1 FROM project_members pm_t
          WHERE pm_t.project_id = t.project_id
            AND pm_t.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            AND pm_t.enabled = TRUE
        ))
        OR (t.visibility = 'private' AND (
          t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          OR EXISTS (
            SELECT 1 FROM task_actors ta_t
            WHERE ta_t.task_id = t.id
              AND ta_t.kind = 'user'
              AND ta_t.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
              AND ta_t.enabled = TRUE
          )
        ))
      )
      AND
      (
        src.visibility = 'public'
        OR (src.visibility = 'project' AND EXISTS (
          SELECT 1 FROM project_members pm_src
          WHERE pm_src.project_id = src.project_id
            AND pm_src.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            AND pm_src.enabled = TRUE
        ))
        OR (src.visibility = 'private' AND (
          src.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          OR EXISTS (
            SELECT 1 FROM task_actors ta_src
            WHERE ta_src.task_id = src.id
              AND ta_src.kind = 'user'
              AND ta_src.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
              AND ta_src.enabled = TRUE
          )
        ))
      )
    )
  )
ORDER BY t.created_at DESC, t.public_id DESC
LIMIT ? OFFSET ?;

-- name: FindRetroDraftAgents :many
-- Resolve the AI agent behind each of a page of retro draft tasks, by
-- looking up the TaskRetroDrafted event (type='task.retro.drafted')
-- joined to ai_agents. Returns the internal task_id alongside the
-- agent's public_id and display name so the caller can index the result
-- by the task_id ListRetroDraftsForWorkspace already handed it.
--
-- The event-derived attribution is the only authoritative source —
-- tasks rows do not carry created_by_agent_id — which is why the main
-- list query stays uncoupled from ai_agents and this runs beside it.
--
-- It takes the whole page at once rather than one task at a time. Per
-- row this was one statement each, so a full page of fifty drafts cost
-- fifty-one round trips to render a list that shows a name next to each
-- title, and the cost grew with the page the operator asked for.
--
-- ROW_NUMBER picks the earliest qualifying event per task, which is the
-- row the per-task query returned under ORDER BY occurred_at, id LIMIT 1.
-- Doing it in the database rather than by keeping the first row seen in
-- Go means the statement returns exactly one row per task: a task whose
-- history holds many drafted events cannot make the result set grow.
SELECT
  ranked.task_id,
  ranked.agent_public_id,
  ranked.agent_name
FROM (
  SELECT
    e.task_id             AS task_id,
    a.public_id           AS agent_public_id,
    a.name                AS agent_name,
    ROW_NUMBER() OVER (
      PARTITION BY e.task_id
      ORDER BY e.occurred_at ASC, e.id ASC
    )                     AS rn
  FROM events e
  INNER JOIN ai_agents a
    ON a.id = e.actor_agent_id
   AND a.enabled = TRUE
  WHERE e.workspace_id = ?
    AND e.task_id IN (sqlc.slice('task_ids'))
    AND e.type         = 'task.retro.drafted'
    AND e.enabled      = TRUE
    AND e.actor_agent_id IS NOT NULL
) ranked
WHERE ranked.rn = 1;
