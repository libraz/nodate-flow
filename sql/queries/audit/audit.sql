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
