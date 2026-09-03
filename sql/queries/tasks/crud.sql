-- name: CreateTask :execlastid
-- Insert a new task. derived_state defaults to 'open' and must NOT be set
-- directly here; the constraint engine and event bus mutate it.
-- Both created_by_user_id and updated_by_user_id are populated with the
-- acting user on insert so that audit projections can render "last touched
-- by" without falling back to the creator when no edit has occurred yet.
INSERT INTO tasks (
  public_id,
  workspace_id,
  project_id,
  parent_task_id,
  created_by_user_id,
  updated_by_user_id,
  task_number,
  title,
  description,
  priority,
  due_on,
  started_on,
  visibility
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindTaskByPublicId :one
-- Detail projection via v_task_detail. Workspace-scoped.
-- task-visibility: not-applicable — every route reaching this statement
-- resolves the task through acl.AuthorizeTaskAccess first, which applies
-- CheckTaskVisibility and answers WS.TASK.NOT_FOUND before the detail row
-- is read. v_task_detail carries no project_id or actor id to write the
-- predicate against in any case.
SELECT
  v.public_id,
  v.workspace_public_id,
  v.project_public_id,
  v.project_name,
  v.parent_task_public_id,
  v.created_by_user_public_id,
  v.title,
  v.description,
  v.visibility,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.completed_at,
  v.project_identifier,
  v.task_number,
  v.archived_at,
  v.agent_memo,
  v.agent_assignee_public_id,
  v.agent_assignee_name,
  v.label_count,
  v.constraint_count,
  v.constraint_satisfied_count,
  v.dependency_count,
  v.actor_count,
  v.sort_weight,
  v.updated_at,
  v.created_at
FROM v_task_detail v
WHERE v.workspace_id = ?
  AND v.public_id = ?
LIMIT 1;

-- name: ListTasksForProject :many
-- List tasks in a project via v_task_list.
--
-- The page total is NOT computed here. COUNT(*) OVER() would make the
-- window function consume every row the filter matches, and each of
-- those rows drags v_task_list's per-row label / assignee subqueries
-- with it -- so asking for a 50-row page of an 8000-task project
-- evaluated them 8000 times instead of 50. CountTasksForProject answers
-- the same question over the bare task rows, and the handler only asks
-- when the page came back full (a short page already pins the total).
SELECT
  v.public_id,
  v.project_public_id,
  v.project_name,
  v.parent_task_public_id,
  v.title,
  v.visibility,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.completed_at,
  v.project_identifier,
  v.task_number,
  v.archived_at,
  v.label_ids,
  v.sort_weight,
  v.updated_at,
  v.created_at,
  v.primary_assignee_public_id,
  v.assignee_count
FROM v_task_list v
WHERE v.workspace_id = ?
  AND v.project_public_id = ?
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR v.visibility = 'public'
    OR (v.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = v.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (v.visibility = 'private' AND (
      v.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = v.task_internal_id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  )
ORDER BY v.sort_weight ASC, v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: CountTasksForProject :one
-- Row count behind ListTasksForProject. Filters must stay identical to
-- that query's WHERE clause or the pager reports a total the list can
-- never reach -- including the Layer-4 visibility predicate, whose
-- absence here would report tasks the caller can never page to.
SELECT COUNT(*)
FROM v_task_list v
WHERE v.workspace_id = ?
  AND v.project_public_id = ?
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR v.visibility = 'public'
    OR (v.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = v.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (v.visibility = 'private' AND (
      v.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = v.task_internal_id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  );

-- name: ListTasksForProjectKeyset :many
-- Keyset-paginated variant of ListTasksForProject.
--
-- Cursor encoding: the caller passes the (created_at, public_id) tuple of
-- the LAST row from the previous page. The first page must pass NULL for
-- both cursor_created_at and cursor_public_id; the IS NULL short-circuit
-- yields the newest rows. ORDER BY is intentionally simpler than the
-- OFFSET-side variant (created_at DESC, public_id DESC only) so the
-- (created_at, public_id) tuple is monotonic and uniquely identifies a
-- cursor position. Sort weight / priority / due_on are NOT considered;
-- callers that need those orderings must keep using the OFFSET variant.
--
-- Index used: idx_tasks_workspace_id_keyset (workspace_id, created_at, public_id).
SELECT
  v.public_id,
  v.project_public_id,
  v.project_name,
  v.parent_task_public_id,
  v.title,
  v.visibility,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.completed_at,
  v.project_identifier,
  v.task_number,
  v.archived_at,
  v.label_ids,
  v.sort_weight,
  v.updated_at,
  v.created_at,
  v.primary_assignee_public_id,
  v.assignee_count
FROM v_task_list v
WHERE v.workspace_id = ?
  AND v.project_public_id = ?
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR v.visibility = 'public'
    OR (v.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = v.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (v.visibility = 'private' AND (
      v.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = v.task_internal_id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  )
  AND (sqlc.narg(cursor_created_at) IS NULL
       OR v.created_at < sqlc.narg(cursor_created_at)
       OR (v.created_at = sqlc.narg(cursor_created_at)
           AND v.public_id < sqlc.narg(cursor_public_id)))
ORDER BY v.created_at DESC, v.public_id DESC
LIMIT ?;

-- name: ListTasksForWorkspace :many
-- List tasks across an entire workspace via v_task_list. See
-- ListTasksForProject for why the total is a separate query.
SELECT
  v.public_id,
  v.project_public_id,
  v.project_name,
  v.parent_task_public_id,
  v.title,
  v.visibility,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.completed_at,
  v.project_identifier,
  v.task_number,
  v.archived_at,
  v.label_ids,
  v.sort_weight,
  v.updated_at,
  v.created_at,
  v.primary_assignee_public_id,
  v.assignee_count
FROM v_task_list v
WHERE v.workspace_id = sqlc.arg('workspace_id')
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR v.visibility = 'public'
    OR (v.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = v.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (v.visibility = 'private' AND (
      v.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = v.task_internal_id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  )
ORDER BY v.sort_weight ASC, v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: CountTasksForWorkspace :one
-- Row count behind ListTasksForWorkspace, carrying the same Layer-4
-- visibility predicate. Dropping any branch of it here would leak the
-- existence of tasks the caller cannot see, as a total larger than the
-- rows they can page through.
SELECT COUNT(*)
FROM v_task_list v
WHERE v.workspace_id = sqlc.arg('workspace_id')
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR v.visibility = 'public'
    OR (v.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = v.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (v.visibility = 'private' AND (
      v.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = v.task_internal_id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  );

-- name: ListTasksForWorkspaceKeyset :many
-- Keyset-paginated variant of ListTasksForWorkspace.
--
-- Cursor encoding: pass the (created_at, public_id) tuple from the LAST
-- row of the previous page. First page passes NULL for both
-- cursor_created_at and cursor_public_id. ORDER BY (created_at DESC,
-- public_id DESC) is monotonic so the tuple comparison uniquely
-- identifies a cursor position. The optional state_filter argument
-- restricts results to a single derived_state when non-empty; pass an
-- empty string to skip the filter.
--
-- Index used: idx_tasks_workspace_id_keyset (workspace_id, created_at, public_id).
SELECT
  v.public_id,
  v.project_public_id,
  v.project_name,
  v.parent_task_public_id,
  v.title,
  v.visibility,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.completed_at,
  v.project_identifier,
  v.task_number,
  v.archived_at,
  v.label_ids,
  v.sort_weight,
  v.updated_at,
  v.created_at,
  v.primary_assignee_public_id,
  v.assignee_count
FROM v_task_list v
WHERE v.workspace_id = ?
  AND (sqlc.arg(state_filter) = '' OR v.derived_state = sqlc.arg(state_filter))
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR v.visibility = 'public'
    OR (v.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = v.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (v.visibility = 'private' AND (
      v.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = v.task_internal_id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  )
  AND (sqlc.narg(cursor_created_at) IS NULL
       OR v.created_at < sqlc.narg(cursor_created_at)
       OR (v.created_at = sqlc.narg(cursor_created_at)
           AND v.public_id < sqlc.narg(cursor_public_id)))
ORDER BY v.created_at DESC, v.public_id DESC
LIMIT ?;

-- name: UpdateTask :execrows
-- Update mutable task fields. derived_state is intentionally NOT writable.
-- updated_by_user_id is appended to the SET list so callers can attribute
-- the edit; pass the acting user's internal id (NULL for system writers).
UPDATE tasks
SET title = ?,
    description = ?,
    priority = ?,
    due_on = ?,
    started_on = ?,
    sort_weight = ?,
    visibility = ?,
    updated_by_user_id = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: UpdateTaskSortWeight :exec
-- Update only the sort_weight for a single task within a workspace.
-- Used by the bulk reorder endpoint inside a transaction.
-- updated_by_user_id is appended so reorder edits are attributed to the
-- acting user (NULL for system writers).
UPDATE tasks
SET sort_weight = ?,
    updated_by_user_id = ?
WHERE id = ?
  AND workspace_id = ?
  AND enabled = TRUE;

-- name: TransitionTaskState :execrows
-- Write the new derived_state computed by the transition handler. This is
-- the only path allowed to mutate derived_state and must be called inside
-- the same transaction as the events append.
-- updated_by_user_id is appended so the audit field reflects who triggered
-- the transition (NULL for system writers / event-bus replays).
UPDATE tasks
SET derived_state = ?,
    completed_at = CASE WHEN ? = 'done' THEN CURRENT_TIMESTAMP ELSE NULL END,
    updated_by_user_id = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: DisableTask :execrows
-- Soft-disable a task.
-- updated_by_user_id is appended so the audit field records who disabled
-- the row (NULL for system writers).
UPDATE tasks
SET enabled = FALSE,
    updated_by_user_id = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: ListMyTasks :many
-- Tasks where the given user is attached as an actor, via v_my_tasks.
-- task-visibility: not-applicable — v_my_tasks emits only rows where the
-- reader holds an enabled task_actors row, and every caller binds its own
-- user_public_id, so the row set is already the private branch of the rule.
SELECT
  v.public_id,
  v.project_public_id,
  v.project_name,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  v.actor_role,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_my_tasks v
WHERE v.workspace_id = ?
  AND v.user_public_id = ?
ORDER BY v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ListMyTasksKeyset :many
-- Keyset-paginated variant of ListMyTasks.
--
-- Cursor encoding: pass the (created_at, public_id) tuple from the LAST
-- row of the previous page. First page passes NULL for both
-- cursor_created_at and cursor_public_id. ORDER BY (created_at DESC,
-- public_id DESC) only — priority / due_on are NOT considered, so the
-- tuple comparison is monotonic. Callers that need priority-aware
-- ordering must keep using the OFFSET variant.
-- task-visibility: not-applicable — v_my_tasks emits only rows where the
-- reader holds an enabled task_actors row, and every caller binds its own
-- user_public_id, so the row set is already the private branch of the rule.
SELECT
  v.public_id,
  v.project_public_id,
  v.project_name,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  v.actor_role,
  v.updated_at,
  v.created_at
FROM v_my_tasks v
WHERE v.workspace_id = ?
  AND v.user_public_id = ?
  AND (sqlc.narg(cursor_created_at) IS NULL
       OR v.created_at < sqlc.narg(cursor_created_at)
       OR (v.created_at = sqlc.narg(cursor_created_at)
           AND v.public_id < sqlc.narg(cursor_public_id)))
ORDER BY v.created_at DESC, v.public_id DESC
LIMIT ?;

-- name: ListMyTasksGlobal :many
-- Cross-workspace variant: tasks where the given user is attached as an
-- actor across every workspace they belong to, joined with the workspace
-- row so the caller gets workspace_public_id / name for grouping. Used by
-- GET /me/tasks to power the cross-workspace "Today" / Calendar views in
-- the web client without fanning out one request per workspace.
-- task-visibility: not-applicable — v_my_tasks emits only rows where the
-- reader holds an enabled task_actors row, and every caller binds its own
-- user_public_id, so the row set is already the private branch of the rule.
SELECT
  v.public_id,
  w.public_id AS workspace_public_id,
  w.name AS workspace_name,
  v.project_public_id,
  v.project_name,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.actor_role,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_my_tasks v
INNER JOIN workspaces w
  ON w.id = v.workspace_id AND w.enabled = TRUE
WHERE v.user_public_id = ?
ORDER BY v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ListMyTasksGlobalKeyset :many
-- Keyset-paginated cross-workspace variant of ListMyTasksGlobal.
--
-- Cursor encoding: pass the (created_at, public_id) tuple from the LAST
-- row of the previous page. First page passes NULL for both
-- cursor_created_at and cursor_public_id. ORDER BY (created_at DESC,
-- public_id DESC) only.
-- task-visibility: not-applicable — v_my_tasks emits only rows where the
-- reader holds an enabled task_actors row, and every caller binds its own
-- user_public_id, so the row set is already the private branch of the rule.
SELECT
  v.public_id,
  w.public_id AS workspace_public_id,
  w.name AS workspace_name,
  v.project_public_id,
  v.project_name,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.actor_role,
  v.updated_at,
  v.created_at
FROM v_my_tasks v
INNER JOIN workspaces w
  ON w.id = v.workspace_id AND w.enabled = TRUE
WHERE v.user_public_id = ?
  AND (sqlc.narg(cursor_created_at) IS NULL
       OR v.created_at < sqlc.narg(cursor_created_at)
       OR (v.created_at = sqlc.narg(cursor_created_at)
           AND v.public_id < sqlc.narg(cursor_public_id)))
ORDER BY v.created_at DESC, v.public_id DESC
LIMIT ?;

-- name: ListMyTasksWithDates :many
-- Cross-workspace variant scoped to tasks whose due_on falls inside the
-- requested [from, to] inclusive date range. Backs the unified flow-web
-- calendar: combined with /me/calendar-events it gives a single round-trip
-- answer for "what is on my plate across every workspace on these days".
-- Undated tasks are excluded; use ListMyTasksGlobal for the planning
-- bucket.
-- task-visibility: not-applicable — v_my_tasks emits only rows where the
-- reader holds an enabled task_actors row, and every caller binds its own
-- user_public_id, so the row set is already the private branch of the rule.
SELECT
  v.public_id,
  w.public_id AS workspace_public_id,
  w.name AS workspace_name,
  v.project_public_id,
  v.project_name,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.actor_role,
  v.updated_at,
  v.created_at,
  COUNT(*) OVER() AS total
FROM v_my_tasks v
INNER JOIN workspaces w
  ON w.id = v.workspace_id AND w.enabled = TRUE
WHERE v.user_public_id = ?
  AND v.due_on IS NOT NULL
  AND v.due_on BETWEEN ? AND ?
ORDER BY
  v.due_on ASC,
  v.priority DESC,
  v.created_at DESC,
  v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ListMyTasksWithDatesKeyset :many
-- Keyset-paginated variant of ListMyTasksWithDates.
--
-- Cursor encoding: pass the (created_at, public_id) tuple from the LAST
-- row of the previous page. First page passes NULL for both
-- cursor_created_at and cursor_public_id. ORDER BY (created_at DESC,
-- public_id DESC) — note this differs from the OFFSET variant which
-- orders by due_on first; the keyset cursor must use a monotonic key,
-- and created_at + public_id is uniquely ordered. Callers that need
-- due_on ordering must keep using the OFFSET variant.
-- task-visibility: not-applicable — v_my_tasks emits only rows where the
-- reader holds an enabled task_actors row, and every caller binds its own
-- user_public_id, so the row set is already the private branch of the rule.
SELECT
  v.public_id,
  w.public_id AS workspace_public_id,
  w.name AS workspace_name,
  v.project_public_id,
  v.project_name,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.actor_role,
  v.updated_at,
  v.created_at
FROM v_my_tasks v
INNER JOIN workspaces w
  ON w.id = v.workspace_id AND w.enabled = TRUE
WHERE v.user_public_id = ?
  AND v.due_on IS NOT NULL
  AND v.due_on BETWEEN ? AND ?
  AND (sqlc.narg(cursor_created_at) IS NULL
       OR v.created_at < sqlc.narg(cursor_created_at)
       OR (v.created_at = sqlc.narg(cursor_created_at)
           AND v.public_id < sqlc.narg(cursor_public_id)))
ORDER BY v.created_at DESC, v.public_id DESC
LIMIT ?;

-- name: ListChildTasksByParentID :many
-- List existing child tasks for a given parent task. Used by step
-- decomposition to avoid suggesting duplicates of already-created steps.
SELECT
  t.id,
  t.public_id,
  t.title,
  t.description,
  t.priority,
  t.derived_state
FROM tasks t
WHERE t.workspace_id = ?
  AND t.parent_task_id = ?
  AND t.enabled = TRUE
  -- Reaching this statement means the actor may see the parent; a child
  -- carries its own visibility and its title and description are on the
  -- wire. Elevated roles skip the check.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR t.visibility = 'public'
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
ORDER BY t.created_at ASC
LIMIT 100;

-- name: ListChildTasksByParentIDKeyset :many
-- Keyset-paginated variant of ListChildTasksByParentID.
--
-- Cursor encoding: pass the (created_at, public_id) tuple from the LAST
-- row of the previous page. First page passes NULL for both
-- cursor_created_at and cursor_public_id. ORDER BY (created_at DESC,
-- public_id DESC) — note this is DESC where the OFFSET variant is ASC,
-- chosen so cursor semantics match the rest of the keyset family
-- ("newer than last page" = strictly less than the cursor tuple).
SELECT
  t.id,
  t.public_id,
  t.title,
  t.description,
  t.priority,
  t.derived_state,
  t.created_at
FROM tasks t
WHERE t.workspace_id = ?
  AND t.parent_task_id = ?
  AND t.enabled = TRUE
  -- Same Layer-4 filter as ListChildTasksByParentID: the parent being
  -- visible says nothing about the children.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR t.visibility = 'public'
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
  AND (sqlc.narg(cursor_created_at) IS NULL
       OR t.created_at < sqlc.narg(cursor_created_at)
       OR (t.created_at = sqlc.narg(cursor_created_at)
           AND t.public_id < sqlc.narg(cursor_public_id)))
ORDER BY t.created_at DESC, t.public_id DESC
LIMIT ?;

-- name: ArchiveTask :execrows
-- Set archived_at on a task.
-- updated_by_user_id is appended so the audit field records who archived
-- the row (NULL for system writers).
UPDATE tasks
SET archived_at = CURRENT_TIMESTAMP,
    updated_by_user_id = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
  AND archived_at IS NULL;

-- name: UnarchiveTask :execrows
-- Clear archived_at on a task.
-- updated_by_user_id is appended so the audit field records who unarchived
-- the row (NULL for system writers).
UPDATE tasks
SET archived_at = NULL,
    updated_by_user_id = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
  AND archived_at IS NOT NULL;

-- name: ListArchivedTasksForWorkspace :many
-- List archived tasks via v_task_list_archived. See ListTasksForProject
-- for why the total is a separate query.
SELECT
  v.public_id,
  v.project_public_id,
  v.project_name,
  v.project_identifier,
  v.task_number,
  v.parent_task_public_id,
  v.title,
  v.visibility,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.completed_at,
  v.archived_at,
  v.sort_weight,
  v.updated_at,
  v.created_at,
  v.primary_assignee_public_id,
  v.assignee_count,
  v.label_ids
FROM v_task_list_archived v
WHERE v.workspace_id = ?
  -- Archiving does not widen who may read a task. The route is mounted
  -- on workspace membership, so without this every member -- guests
  -- included -- reads the titles of archived private and project tasks.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR v.visibility = 'public'
    OR (v.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = v.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (v.visibility = 'private' AND (
      v.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = v.task_internal_id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  )
ORDER BY v.archived_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: CountArchivedTasksForWorkspace :one
-- Row count behind ListArchivedTasksForWorkspace, carrying the same
-- Layer-4 visibility predicate. A total taken without it discloses how
-- many archived tasks the caller is not allowed to see.
SELECT COUNT(*)
FROM v_task_list_archived v
WHERE v.workspace_id = ?
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR v.visibility = 'public'
    OR (v.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = v.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (v.visibility = 'private' AND (
      v.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = v.task_internal_id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  );

-- name: ListArchivedTasksForWorkspaceKeyset :many
-- Keyset-paginated variant of ListArchivedTasksForWorkspace.
--
-- Cursor encoding: pass the (archived_at, public_id) tuple from the
-- LAST row of the previous page. First page passes NULL for both
-- cursor_archived_at and cursor_public_id. ORDER BY (archived_at DESC,
-- public_id DESC) matches the OFFSET variant; archived_at + public_id
-- is monotonically unique on this projection because v_task_list_archived
-- only emits rows with archived_at IS NOT NULL.
SELECT
  v.public_id,
  v.project_public_id,
  v.project_name,
  v.project_identifier,
  v.task_number,
  v.parent_task_public_id,
  v.title,
  v.visibility,
  v.derived_state,
  v.priority,
  v.due_on,
  v.started_on,
  v.completed_at,
  v.archived_at,
  v.sort_weight,
  v.updated_at,
  v.created_at,
  v.primary_assignee_public_id,
  v.assignee_count,
  v.label_ids
FROM v_task_list_archived v
WHERE v.workspace_id = ?
  -- Same Layer-4 filter as ListArchivedTasksForWorkspace.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR v.visibility = 'public'
    OR (v.visibility = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm_vis
      WHERE pm_vis.project_id = v.project_id
        AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        AND pm_vis.enabled = TRUE
    ))
    OR (v.visibility = 'private' AND (
      v.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
      OR EXISTS (
        SELECT 1 FROM task_actors ta_vis
        WHERE ta_vis.task_id = v.task_internal_id
          AND ta_vis.kind = 'user'
          AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND ta_vis.enabled = TRUE
      )
    ))
  )
  AND (sqlc.narg(cursor_archived_at) IS NULL
       OR v.archived_at < sqlc.narg(cursor_archived_at)
       OR (v.archived_at = sqlc.narg(cursor_archived_at)
           AND v.public_id < sqlc.narg(cursor_public_id)))
ORDER BY v.archived_at DESC, v.public_id DESC
LIMIT ?;

-- name: LockProjectForTaskNumber :one
-- Lock the owning project row before task-number allocation. This serializes
-- concurrent creators for the same project while still allowing different
-- projects to allocate independently.
SELECT id
FROM projects
WHERE workspace_id = ?
  AND id = ?
  AND enabled = TRUE
FOR UPDATE;

-- name: AssignTaskNumber :one
-- Allocate the next task number for a project. Must be called inside a
-- transaction after LockProjectForTaskNumber.
-- workspace_id is included so the index (workspace_id, project_id) is used and
-- the query is bounded to the caller's workspace as a defence-in-depth check.
SELECT COALESCE(MAX(task_number), 0) + 1 AS next_number
FROM tasks
WHERE workspace_id = ?
  AND project_id = ?;

-- name: SetTaskNumber :exec
-- Set the task_number after allocation.
-- updated_by_user_id is appended so the audit field records who allocated
-- the number; in practice this runs in the same transaction as CreateTask
-- so the same actor id is reused (NULL for system writers).
-- workspace_id is required to ensure the update never crosses workspace
-- boundaries even if the caller passes a foreign tasks.id.
UPDATE tasks
SET task_number = ?,
    updated_by_user_id = ?
WHERE id = ?
  AND workspace_id = ?;

-- name: ResolveTaskRef :one
-- Resolve a human-readable task reference (e.g. NF-42) to a task public_id.
-- task-visibility: not-applicable — the only caller re-resolves the task it
-- finds through the shared task ACL and returns the error before the title
-- reaches the caller, so a task the actor may not see is answered as missing.
SELECT
  t.id,
  t.public_id,
  t.workspace_id,
  t.title
FROM tasks t
INNER JOIN projects p ON p.id = t.project_id AND p.enabled = TRUE
WHERE p.workspace_id = ?
  AND p.identifier = ?
  AND t.task_number = ?
  AND t.enabled = TRUE
LIMIT 1;
