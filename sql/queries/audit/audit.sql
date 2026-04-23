-- name: AppendAuditLog :execlastid
-- Append a workspace-scoped audit row. metadata_json MUST be redacted.
INSERT INTO audit_logs (
  public_id,
  workspace_id,
  actor_user_id,
  action,
  resource_type,
  resource_public_id,
  ip_address,
  user_agent,
  metadata_json,
  occurred_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListRecentAudit :many
-- List recent audit log entries for a workspace via v_audit_recent.
SELECT
  v.public_id,
  v.actor_user_public_id,
  v.actor_display_name,
  v.action,
  v.resource_type,
  v.resource_public_id,
  v.ip_address,
  v.user_agent,
  v.metadata_json,
  v.occurred_at,
  COUNT(*) OVER() AS total
FROM v_audit_recent v
WHERE v.workspace_id = ?
ORDER BY v.occurred_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;

-- name: ListWorkspaceAuditLogs :many
-- Paginated workspace audit log with optional action / resource_type /
-- actor-search / date-range filters for the workspace-admin UI.
--   filter_action: pass '' to skip, otherwise exact match on al.action.
--   filter_resource_type: pass '' to skip, otherwise exact match on al.resource_type.
--   filter_actor_search: pass '' to skip, otherwise substring match against
--     the actor user's display_name or email.
--   filter_from / filter_to: pass NULL to skip each bound (inclusive).
SELECT
  al.public_id,
  actor.public_id AS actor_user_public_id,
  actor.display_name AS actor_display_name,
  al.action,
  al.resource_type,
  al.resource_public_id,
  al.ip_address,
  al.user_agent,
  al.metadata_json,
  al.occurred_at,
  COUNT(*) OVER() AS total
FROM audit_logs al
LEFT JOIN users actor
  ON actor.id = al.actor_user_id AND actor.enabled = TRUE
WHERE al.workspace_id = ?
  AND al.enabled = TRUE
  AND (sqlc.arg(filter_action) = '' OR al.action = sqlc.arg(filter_action))
  AND (sqlc.arg(filter_resource_type) = '' OR al.resource_type = sqlc.arg(filter_resource_type))
  AND (sqlc.arg(filter_actor_search) = ''
       OR actor.display_name LIKE CONCAT('%', sqlc.arg(filter_actor_search), '%')
       OR actor.email LIKE CONCAT('%', sqlc.arg(filter_actor_search), '%'))
  AND (sqlc.narg(filter_from) IS NULL OR al.occurred_at >= sqlc.narg(filter_from))
  AND (sqlc.narg(filter_to) IS NULL OR al.occurred_at <= sqlc.narg(filter_to))
ORDER BY al.occurred_at DESC, al.public_id DESC
LIMIT ? OFFSET ?;

-- name: AppendInstanceAuditLog :execlastid
-- Append an instance-wide audit row. payload_json MUST be redacted.
INSERT INTO instance_audit_logs (
  public_id,
  actor_user_id,
  action,
  target_workspace_id,
  target_resource_type,
  target_resource_public_id,
  ip_address,
  user_agent,
  payload_json,
  occurred_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
