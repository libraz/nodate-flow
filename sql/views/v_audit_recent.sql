-- v_audit_recent
-- Recent audit events projected for the admin UI. api_key values are
-- never present here; metadata_json is already redacted at write time.
CREATE OR REPLACE VIEW v_audit_recent AS
SELECT
  al.workspace_id,
  al.public_id,
  actor.public_id AS actor_user_public_id,
  actor.display_name AS actor_display_name,
  al.action,
  al.resource_type,
  al.resource_public_id,
  al.ip_address,
  al.user_agent,
  al.metadata_json,
  al.occurred_at
FROM audit_logs al
INNER JOIN workspaces w
  ON w.id = al.workspace_id AND w.enabled = TRUE
LEFT JOIN users actor
  ON actor.id = al.actor_user_id AND actor.enabled = TRUE
WHERE al.enabled = TRUE;
