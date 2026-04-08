-- name: LogAiInvocation :execlastid
-- Append a redacted record of an LLM call. Both prompt_redacted and
-- response_redacted MUST already be filtered through the redaction layer.
INSERT INTO ai_invocations (
  public_id,
  workspace_id,
  provider_id,
  user_id,
  task_id,
  purpose,
  model,
  prompt_redacted,
  response_redacted,
  tokens_input,
  tokens_output,
  cost_estimate,
  status,
  error_code,
  invoked_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAiInvocationsForWorkspace :many
-- Recent redacted LLM call records for a workspace, newest first. Used
-- by the AI activity panel. All columns here are already redacted at
-- write time by the orchestrator; safe to surface at the API boundary.
SELECT
  public_id,
  purpose,
  model,
  prompt_redacted,
  response_redacted,
  tokens_input,
  tokens_output,
  cost_estimate,
  status,
  error_code,
  invoked_at
FROM ai_invocations
WHERE workspace_id = ?
ORDER BY invoked_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: ListAiInvocationsForTask :many
-- Recent redacted LLM call records scoped to a single task. Used by
-- the task detail AI reasoning panel (2.WEB-2). workspace_id is
-- included so tenant isolation is enforced at the query level.
SELECT
  public_id,
  purpose,
  model,
  prompt_redacted,
  response_redacted,
  tokens_input,
  tokens_output,
  cost_estimate,
  status,
  error_code,
  invoked_at
FROM ai_invocations
WHERE workspace_id = ?
  AND task_id = ?
ORDER BY invoked_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: SumAiCostTodayForWorkspace :one
-- Sum the estimated cost (in whole cents) of LLM calls made today for a
-- workspace. cost_estimate is stored as DECIMAL(10,6) USD; multiply by 100
-- and round to produce a cent-scale integer suitable for CostGuard.
SELECT CAST(COALESCE(ROUND(SUM(cost_estimate) * 100), 0) AS SIGNED) AS total_cents
FROM ai_invocations
WHERE workspace_id = ?
  AND invoked_at >= ?;
