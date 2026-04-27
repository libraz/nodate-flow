-- name: ListWorkspaceActivity :many
-- Cursor-paginated workspace activity timeline drawn from
-- v_workspace_activity (audit_logs UNION ALL ai_invocations UNION ALL
-- mcp_invocations).
--
-- Filters:
--   filter_source: pass '' to skip, otherwise exact match on source ('audit'|'ai'|'mcp').
--   filter_since:  pass NULL to skip, otherwise inclusive lower bound on occurred_at.
--   filter_until:  pass NULL to skip, otherwise inclusive upper bound on occurred_at.
--
-- Cursor:
--   cursor_occurred_at / cursor_public_id: pass NULL to skip. When supplied,
--     the page is restricted to rows strictly before the cursor in
--     (occurred_at DESC, public_id DESC) order.
SELECT
  va.public_id,
  va.source,
  va.source_table,
  va.occurred_at,
  va.actor_user_public_id,
  va.actor_kind,
  va.action,
  va.resource_type,
  va.resource_public_id,
  va.severity
FROM v_workspace_activity va
WHERE va.workspace_id = ?
  AND (sqlc.arg(filter_source) = '' OR va.source = sqlc.arg(filter_source))
  AND (sqlc.narg(filter_since) IS NULL OR va.occurred_at >= sqlc.narg(filter_since))
  AND (sqlc.narg(filter_until) IS NULL OR va.occurred_at <= sqlc.narg(filter_until))
  AND (
    sqlc.narg(cursor_occurred_at) IS NULL
    OR va.occurred_at < sqlc.narg(cursor_occurred_at)
    OR (va.occurred_at = sqlc.narg(cursor_occurred_at) AND va.public_id < sqlc.narg(cursor_public_id))
  )
ORDER BY va.occurred_at DESC, va.public_id DESC
LIMIT ?;

-- name: CountWorkspaceActivity :one
-- Count of v_workspace_activity rows matching the same filters as
-- ListWorkspaceActivity (no cursor).
SELECT COUNT(*) AS total
FROM v_workspace_activity va
WHERE va.workspace_id = ?
  AND (sqlc.arg(filter_source) = '' OR va.source = sqlc.arg(filter_source))
  AND (sqlc.narg(filter_since) IS NULL OR va.occurred_at >= sqlc.narg(filter_since))
  AND (sqlc.narg(filter_until) IS NULL OR va.occurred_at <= sqlc.narg(filter_until));
