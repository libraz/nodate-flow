-- name: LogAiInvocation :execlastid
-- Append a redacted record of an LLM call. Both prompt_redacted and
-- response_redacted MUST already be filtered through the redaction layer.
INSERT INTO ai_invocations (
  public_id,
  workspace_id,
  provider_id,
  user_id,
  agent_id,
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
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAiInvocationsForWorkspace :many
-- Recent redacted LLM call records for a workspace, newest first. Used
-- by the AI activity panel. All columns here are already redacted at
-- write time by the orchestrator; safe to surface at the API boundary.
--
-- Redaction removes secrets, not task content: a prompt carries the
-- titles and descriptions it was built from. So a row about a task is
-- returned only when the reader may read that task (a deleted one reads
-- as unreadable), and a row with no task — a prompt that may embed
-- several — only to the user who made the call. Elevated roles see all.
SELECT
  ai.public_id,
  ai.purpose,
  ai.model,
  ai.prompt_redacted,
  ai.response_redacted,
  ai.tokens_input,
  ai.tokens_output,
  ai.cost_estimate,
  ai.status,
  ai.error_code,
  ai.invoked_at
FROM ai_invocations ai
LEFT JOIN tasks t ON t.id = ai.task_id AND t.enabled = TRUE
WHERE ai.workspace_id = sqlc.arg('workspace_id')
  AND (
    CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
    OR (ai.task_id IS NULL AND ai.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED))
    OR (t.id IS NOT NULL AND (
      t.visibility = 'public'
      OR (t.visibility = 'project' AND EXISTS (
        SELECT 1 FROM project_members pm_vis
        WHERE pm_vis.project_id = t.project_id
          AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND pm_vis.enabled = TRUE
      ))
      OR (t.visibility = 'private' AND (
        t.created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        OR EXISTS (
          SELECT 1 FROM task_actors ta_vis
          WHERE ta_vis.task_id = t.id
            AND ta_vis.kind = 'user'
            AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            AND ta_vis.enabled = TRUE
        )
      ))
    ))
  )
ORDER BY ai.invoked_at DESC, ai.id DESC
LIMIT ? OFFSET ?;

-- name: ListAiInvocationsForTask :many
-- Recent redacted LLM call records scoped to a single task. Used by
-- the task detail AI reasoning panel. workspace_id is
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
-- Sum the estimated cost (in whole cents) of chat/agent LLM calls made today
-- for a workspace. cost_estimate is stored as DECIMAL(10,6) USD; multiply by
-- 100 and round to produce a cent-scale integer suitable for CostGuard.
-- Embedding calls are excluded here: they have their own
-- ai_settings.embed_budget_cents_day bucket (see SumEmbedCostTodayForWorkspace)
-- and must not be double-counted against the chat/agent daily budget.
SELECT CAST(COALESCE(ROUND(SUM(cost_estimate) * 100), 0) AS SIGNED) AS total_cents
FROM ai_invocations
WHERE workspace_id = ?
  AND purpose NOT LIKE 'embed\_%'
  AND invoked_at >= ?;

-- name: SumEmbedCostTodayForWorkspace :one
-- Sum the estimated cost (in whole cents) of embedding calls made today for a
-- workspace. Embeddings have their own ai_settings.embed_budget_cents_day
-- bucket, separate from chat/agent LLM budget.
SELECT CAST(COALESCE(ROUND(SUM(cost_estimate) * 100), 0) AS SIGNED) AS total_cents
FROM ai_invocations
WHERE workspace_id = ?
  AND purpose LIKE 'embed\_%'
  AND invoked_at >= ?;

-- name: SumAiCostForAgentSince :one
-- Sum the estimated cost (cents) of LLM calls attributed to a given AI
-- agent since a lower bound. Used by agentguard to enforce the
-- agent's monthly cost cap.
SELECT CAST(COALESCE(ROUND(SUM(cost_estimate) * 100), 0) AS SIGNED) AS total_cents
FROM ai_invocations
WHERE agent_id = ?
  AND invoked_at >= ?;
