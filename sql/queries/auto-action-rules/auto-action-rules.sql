-- ============================================================================
-- auto_action_rules queries
-- Per-workspace auto-action rule configuration. Each row defines a single
-- rule kind (e.g. auto-close, auto-archive) with its own enabled flag,
-- confidence threshold, and idle-hours delay. Rules can now be scoped per
-- signal_kind; see docs/conventions/autonomy.md for resolution layer order.
-- Rows may also carry an explicit autonomy_level ('suggest' | 'draft' |
-- 'auto'); when non-NULL the resolver returns it verbatim and skips the
-- confidence-vs-threshold gate. NULL preserves the legacy confidence-based
-- derivation, so callers must be able to write NULL to clear an override.
-- ============================================================================

-- name: ListAutoActionRulesForWorkspace :many
-- List all auto-action rules for a workspace, ordered by kind with
-- signal_kind as a deterministic tie-breaker (NULL/wildcard rows last so
-- specific rules surface before fallbacks).
SELECT
  id,
  public_id,
  workspace_id,
  kind,
  signal_kind,
  enabled,
  confidence,
  idle_hours,
  autonomy_level,
  updated_at,
  created_at
FROM auto_action_rules
WHERE workspace_id = ?
ORDER BY kind ASC, signal_kind IS NULL ASC, signal_kind ASC;

-- name: UpsertAutoActionRule :exec
-- Insert or update a single auto-action rule for a workspace.
-- The UNIQUE KEY on (workspace_id, kind, signal_kind_match) makes this
-- idempotent. A different signal_kind targets a distinct row by design, so
-- signal_kind is intentionally excluded from the UPDATE branch.
INSERT INTO auto_action_rules (public_id, workspace_id, kind, signal_kind, enabled, confidence, idle_hours, autonomy_level)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  enabled        = VALUES(enabled),
  confidence     = VALUES(confidence),
  idle_hours     = VALUES(idle_hours),
  autonomy_level = VALUES(autonomy_level);

-- name: MatchAutoActionRule :one
-- Resolve the most specific auto-action rule for a (workspace, kind, signal_kind)
-- triple. Ordering:
--   1) exact signal_kind match (signal_kind = sqlc.arg(signal_kind))
--   2) wildcard-prefix rules where the stored value starts with '*.' and the
--      remainder is a dotted-suffix of the caller's signal_kind (e.g. stored
--      '*.presence' matches caller 'discord.presence' via LIKE comparison
--      against CONCAT('%.', SUBSTRING(signal_kind, 3))).
--   3) wildcard fallback (signal_kind IS NULL).
-- Returns at most one row; if multiple wildcard rules tie, fall back to
-- created_at ASC then id ASC for determinism.
SELECT
  id,
  public_id,
  workspace_id,
  kind,
  signal_kind,
  enabled,
  confidence,
  idle_hours,
  autonomy_level,
  updated_at,
  created_at
FROM auto_action_rules
WHERE workspace_id = sqlc.arg(workspace_id)
  AND kind = sqlc.arg(kind)
  AND enabled = TRUE
  AND (
    signal_kind = sqlc.arg(signal_kind)
    OR (signal_kind LIKE '*.%' AND sqlc.arg(signal_kind) LIKE CONCAT('%.', SUBSTRING(signal_kind, 3)))
    OR signal_kind IS NULL
  )
ORDER BY
  CASE
    WHEN signal_kind = sqlc.arg(signal_kind) THEN 0
    WHEN signal_kind LIKE '*.%' THEN 1
    ELSE 2
  END ASC,
  created_at ASC,
  id ASC
LIMIT 1;
