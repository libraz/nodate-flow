-- name: AdminListInstanceAuditLogs :many
-- Paginated instance audit log with optional action and date filters.
-- filter_action: pass '' to skip, otherwise exact match on action.
-- filter_from / filter_to: pass NULL to skip date bounds.
SELECT
  v.public_id,
  v.actor_user_public_id,
  v.actor_display_name,
  v.action,
  v.target_workspace_public_id,
  v.target_workspace_name,
  v.target_resource_type,
  v.target_resource_public_id,
  v.ip_address,
  v.user_agent,
  v.payload_json,
  v.occurred_at,
  COUNT(*) OVER() AS total
FROM v_instance_audit_logs v
WHERE (? = '' OR v.action = ?)
  AND (? IS NULL OR v.occurred_at >= ?)
  AND (? IS NULL OR v.occurred_at <= ?)
ORDER BY v.occurred_at DESC, v.public_id DESC
LIMIT ? OFFSET ?;
