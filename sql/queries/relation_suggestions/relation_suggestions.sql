-- name: CreateRelationSuggestion :execlastid
-- Insert an AI-generated relation suggestion between two tasks.
INSERT INTO relation_suggestions (
  public_id,
  workspace_id,
  source_task_id,
  target_task_id,
  suggested_kind,
  confidence
) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListPendingSuggestionsForTask :many
-- List pending suggestions where a task is either the source or target.
-- Joins tasks for human-readable titles and public IDs.
SELECT
  rs.public_id,
  rs.suggested_kind,
  rs.confidence,
  rs.status,
  st.public_id AS source_task_public_id,
  st.title AS source_task_title,
  tt.public_id AS target_task_public_id,
  tt.title AS target_task_title,
  rs.created_at
FROM relation_suggestions rs
INNER JOIN tasks st ON st.id = rs.source_task_id AND st.enabled = TRUE
INNER JOIN tasks tt ON tt.id = rs.target_task_id AND tt.enabled = TRUE
WHERE rs.workspace_id = sqlc.arg('workspace_id')
  AND (rs.source_task_id = sqlc.arg('source_task_id') OR rs.target_task_id = sqlc.arg('target_task_id'))
  AND rs.status = 'pending'
  -- Both titles are on the wire, so a suggestion is listable only when
  -- the actor may see both ends. Elevated roles skip the check.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR (
      (
        st.visibility = 'public'
        OR (st.visibility = 'project' AND EXISTS (
          SELECT 1 FROM project_members pm_st
          WHERE pm_st.project_id = st.project_id
            AND pm_st.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            AND pm_st.enabled = TRUE
        ))
        OR (st.visibility = 'private' AND (
          st.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          OR EXISTS (
            SELECT 1 FROM task_actors ta_st
            WHERE ta_st.task_id = st.id
              AND ta_st.kind = 'user'
              AND ta_st.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
              AND ta_st.enabled = TRUE
          )
        ))
      )
      AND
      (
        tt.visibility = 'public'
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
    )
  )
ORDER BY rs.confidence DESC, rs.id DESC
LIMIT ?;

-- name: ListPendingSuggestionsForWorkspace :many
-- List pending suggestions for a workspace with pagination.
SELECT
  rs.public_id,
  rs.suggested_kind,
  rs.confidence,
  rs.status,
  st.public_id AS source_task_public_id,
  st.title AS source_task_title,
  tt.public_id AS target_task_public_id,
  tt.title AS target_task_title,
  rs.created_at,
  COUNT(*) OVER() AS total
FROM relation_suggestions rs
INNER JOIN tasks st ON st.id = rs.source_task_id AND st.enabled = TRUE
INNER JOIN tasks tt ON tt.id = rs.target_task_id AND tt.enabled = TRUE
WHERE rs.workspace_id = sqlc.arg('workspace_id')
  AND rs.status = 'pending'
  -- Both titles are on the wire, so a suggestion is listable only when
  -- the actor may see both ends. Elevated roles skip the check.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR (
      (
        st.visibility = 'public'
        OR (st.visibility = 'project' AND EXISTS (
          SELECT 1 FROM project_members pm_st
          WHERE pm_st.project_id = st.project_id
            AND pm_st.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            AND pm_st.enabled = TRUE
        ))
        OR (st.visibility = 'private' AND (
          st.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          OR EXISTS (
            SELECT 1 FROM task_actors ta_st
            WHERE ta_st.task_id = st.id
              AND ta_st.kind = 'user'
              AND ta_st.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
              AND ta_st.enabled = TRUE
          )
        ))
      )
      AND
      (
        tt.visibility = 'public'
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
    )
  )
ORDER BY rs.created_at DESC, rs.id DESC
LIMIT ? OFFSET ?;

-- name: GetSuggestionByPublicId :one
-- Fetch a single suggestion by public_id with source/target task info.
SELECT
  rs.id,
  rs.public_id,
  rs.workspace_id,
  rs.source_task_id,
  rs.target_task_id,
  rs.suggested_kind,
  rs.confidence,
  rs.status,
  rs.resolved_by,
  rs.resolved_at,
  st.public_id AS source_task_public_id,
  st.title AS source_task_title,
  tt.public_id AS target_task_public_id,
  tt.title AS target_task_title,
  rs.created_at
FROM relation_suggestions rs
INNER JOIN tasks st ON st.id = rs.source_task_id
INNER JOIN tasks tt ON tt.id = rs.target_task_id
WHERE rs.workspace_id = sqlc.arg('workspace_id')
  AND rs.public_id = sqlc.arg('public_id')
  -- Both titles are on the wire, so the suggestion is readable only when
  -- the actor may see both ends -- the same filter the two list queries
  -- carry. Elevated roles skip the check.
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR (
      (
        st.visibility = 'public'
        OR (st.visibility = 'project' AND EXISTS (
          SELECT 1 FROM project_members pm_st
          WHERE pm_st.project_id = st.project_id
            AND pm_st.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            AND pm_st.enabled = TRUE
        ))
        OR (st.visibility = 'private' AND (
          st.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          OR EXISTS (
            SELECT 1 FROM task_actors ta_st
            WHERE ta_st.task_id = st.id
              AND ta_st.kind = 'user'
              AND ta_st.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
              AND ta_st.enabled = TRUE
          )
        ))
      )
      AND
      (
        tt.visibility = 'public'
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
    )
  )
LIMIT 1;

-- name: ResolveSuggestion :execrows
-- Accept or dismiss a pending suggestion. Only transitions from 'pending'.
UPDATE relation_suggestions
SET status = ?,
    resolved_by = ?,
    resolved_at = NOW(3)
WHERE workspace_id = ?
  AND public_id = ?
  AND status = 'pending';

-- name: DismissAllForTask :exec
-- Bulk-dismiss all pending suggestions involving a specific task.
UPDATE relation_suggestions
SET status = 'dismissed',
    resolved_by = ?,
    resolved_at = NOW(3)
WHERE workspace_id = ?
  AND (source_task_id = ? OR target_task_id = ?)
  AND status = 'pending';
