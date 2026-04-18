-- ============================================================================
-- ai_settings queries (ADR 0003)
-- Per-workspace AI knobs: embed model, daily embed budget, and the
-- duplicate-detection similarity thresholds.
-- ============================================================================

-- name: GetAiSettings :one
-- Fetch the ai_settings row for a workspace. Returns sql.ErrNoRows when the
-- workspace has never written a row; the caller should fall back to the
-- column defaults (mock-768 / 100 cents/day / 0.870 / 0.750).
SELECT
  id,
  workspace_id,
  embed_model,
  embed_budget_cents_day,
  duplicate_threshold_high,
  duplicate_threshold_low,
  auto_action_enabled,
  auto_action_interval_minutes,
  auto_action_threshold,
  updated_at,
  created_at
FROM ai_settings
WHERE workspace_id = ?
LIMIT 1;

-- name: UpsertAiSettings :exec
-- Create or update the ai_settings row for a workspace. The UNIQUE KEY on
-- workspace_id makes this idempotent.
INSERT INTO ai_settings (
  workspace_id,
  embed_model,
  embed_budget_cents_day,
  duplicate_threshold_high,
  duplicate_threshold_low,
  auto_action_enabled,
  auto_action_interval_minutes,
  auto_action_threshold
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  embed_model = VALUES(embed_model),
  embed_budget_cents_day = VALUES(embed_budget_cents_day),
  duplicate_threshold_high = VALUES(duplicate_threshold_high),
  duplicate_threshold_low = VALUES(duplicate_threshold_low),
  auto_action_enabled = VALUES(auto_action_enabled),
  auto_action_interval_minutes = VALUES(auto_action_interval_minutes),
  auto_action_threshold = VALUES(auto_action_threshold);
