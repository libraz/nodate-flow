-- v_instance_audit_logs
-- Instance-wide audit log joined with actor user and optional workspace.
-- Disabled actor / workspace rows are masked (LEFT JOIN with enabled = TRUE
-- on the ON clause). If an audit consumer needs to surface forensic data
-- for soft-disabled tenants, query instance_audit_logs directly rather than
-- through this view.
CREATE OR REPLACE VIEW v_instance_audit_logs AS
SELECT
  ial.public_id,
  actor.public_id   AS actor_user_public_id,
  actor.display_name AS actor_display_name,
  ial.action,
  ws.public_id      AS target_workspace_public_id,
  ws.name           AS target_workspace_name,
  ial.target_resource_type,
  ial.target_resource_public_id,
  ial.ip_address,
  ial.user_agent,
  ial.payload_json,
  ial.occurred_at
FROM instance_audit_logs ial
LEFT JOIN users actor ON actor.id = ial.actor_user_id AND actor.enabled = TRUE
LEFT JOIN workspaces ws ON ws.id = ial.target_workspace_id AND ws.enabled = TRUE
WHERE ial.enabled = TRUE;
