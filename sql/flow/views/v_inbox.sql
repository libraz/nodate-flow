-- v_inbox
-- Aggregated inbox: signals that have arrived in a workspace with optional
-- task linkage. Consumers filter by workspace_id and archive/snooze state.
CREATE OR REPLACE VIEW v_inbox AS
SELECT
  s.workspace_id,
  w.public_id AS workspace_public_id,
  s.public_id,
  t.public_id AS task_public_id,
  t.title AS task_title,
  s.source,
  s.kind,
  s.external_id,
  s.payload_json,
  s.received_at,
  s.updated_at,
  s.created_at
FROM signals s
INNER JOIN workspaces w
  ON w.id = s.workspace_id AND w.enabled = TRUE
LEFT JOIN tasks t
  ON t.id = s.task_id AND t.enabled = TRUE
WHERE s.enabled = TRUE;
