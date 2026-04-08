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

-- name: SumAiCostTodayForWorkspace :one
-- Sum the estimated cost (in whole cents) of LLM calls made today for a
-- workspace. cost_estimate is stored as DECIMAL(10,6) USD; multiply by 100
-- and round to produce a cent-scale integer suitable for CostGuard.
SELECT CAST(COALESCE(ROUND(SUM(cost_estimate) * 100), 0) AS SIGNED) AS total_cents
FROM ai_invocations
WHERE workspace_id = ?
  AND invoked_at >= ?;
