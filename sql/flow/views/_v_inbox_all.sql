-- v_inbox_all
-- Signals that belong to a workspace, with optional task linkage, whether
-- or not they have arrived yet. A future received_at is an inbox snooze,
-- which hides the signal from the inbox (v_inbox) but not from its task.
CREATE OR REPLACE ALGORITHM=MERGE VIEW v_inbox_all AS
SELECT
  s.workspace_id,
  w.public_id AS workspace_public_id,
  s.public_id,
  t.public_id AS task_public_id,
  t.title AS task_title,
  -- The task's own ACL columns ride along so a consumer can apply the
  -- Layer 4 visibility rule. The view cannot apply it itself: it takes
  -- no parameters, and the answer depends on who is reading. Exposing
  -- the inputs here is what lets every consumer reach the same verdict
  -- instead of each inventing one.
  t.visibility AS task_visibility,
  t.project_id AS task_project_id,
  t.created_by_user_id AS task_created_by_user_id,
  t.id AS task_internal_id,
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
