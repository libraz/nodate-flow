-- ============================================================================
-- auto_action_rules queries
-- Per-workspace auto-action rule configuration. Each row defines a single
-- rule kind (e.g. auto-close, auto-archive) with its own enabled flag,
-- confidence threshold, and idle-hours delay.
-- ============================================================================

-- name: ListAutoActionRulesForWorkspace :many
-- List all auto-action rules for a workspace, ordered by kind.
SELECT
  id,
  public_id,
  workspace_id,
  kind,
  enabled,
  confidence,
  idle_hours,
  updated_at,
  created_at
FROM auto_action_rules
WHERE workspace_id = ?
ORDER BY kind ASC;

-- name: UpsertAutoActionRule :exec
-- Insert or update a single auto-action rule for a workspace.
-- The UNIQUE KEY on (workspace_id, kind) makes this idempotent.
INSERT INTO auto_action_rules (public_id, workspace_id, kind, enabled, confidence, idle_hours)
VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  enabled    = VALUES(enabled),
  confidence = VALUES(confidence),
  idle_hours = VALUES(idle_hours);
