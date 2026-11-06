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
  audience,
  input_resources_json,
  input_count,
  provenance_valid,
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
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: AddAiInvocationInput :exec
-- Record one canonical task public ID included in the provider input. The
-- caller inserts the invocation and all of its inputs in one transaction.
INSERT INTO ai_invocations_inputs (invocation_id, task_public_id)
VALUES (?, ?);

-- name: ListAiInvocationsForWorkspace :many
-- Recent redacted LLM calls, newest first. Redaction removes secrets,
-- not task content. Check every public resource reference and the subject
-- task against the reader's current Layer-4 ACL. Missing, deleted,
-- cross-workspace, malformed, or unknown resource references fail closed.
WITH invocation_policy AS (
  SELECT
    CAST(sqlc.arg('is_elevated') AS SIGNED) AS is_elevated,
    CAST(sqlc.arg('actor_user_id') AS UNSIGNED) AS actor_user_id
)
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
CROSS JOIN invocation_policy inv_acl
LEFT JOIN tasks t ON t.id = ai.task_id AND t.enabled = TRUE
WHERE ai.workspace_id = sqlc.arg('workspace_id')
  AND (
    inv_acl.is_elevated = 1
    OR (
      ai.provenance_valid = TRUE
      AND JSON_TYPE(ai.input_resources_json) = 'ARRAY'
      AND ai.input_count = JSON_LENGTH(ai.input_resources_json)
      AND ai.input_count = (
        SELECT COUNT(*)
        FROM ai_invocations_inputs counted_input
        WHERE counted_input.invocation_id = ai.id
      )
      AND NOT EXISTS (
        SELECT 1
        FROM ai_invocations_inputs input_ref
        LEFT JOIN tasks input_task
          ON input_task.workspace_id = ai.workspace_id
          AND input_task.public_id = input_ref.task_public_id
          AND input_task.enabled = TRUE
        WHERE input_ref.invocation_id = ai.id
          AND (
            input_task.id IS NULL
            OR (
              input_task.visibility <> 'public'
              AND NOT (
                input_task.visibility = 'project' AND EXISTS (
                  SELECT 1 FROM project_members pm_input
                  WHERE pm_input.project_id = input_task.project_id
                    AND pm_input.user_id = inv_acl.actor_user_id
                    AND pm_input.enabled = TRUE
                )
              )
              AND (
                input_task.visibility = 'private'
                AND (
                  input_task.created_by_user_id = inv_acl.actor_user_id
                  OR EXISTS (
                    SELECT 1 FROM task_actors ta_input
                    WHERE ta_input.task_id = input_task.id
                      AND ta_input.kind = 'user'
                      AND ta_input.user_id = inv_acl.actor_user_id
                      AND ta_input.enabled = TRUE
                  )
                )
              ) IS NOT TRUE
            )
            OR (ai.audience = 'workspace' AND input_task.visibility <> 'public')
          )
      )
      AND (
        (
          ai.audience = 'initiator'
          AND ai.user_id = inv_acl.actor_user_id
          AND (
            ai.task_id IS NULL
            OR (t.id IS NOT NULL AND (
              t.visibility = 'public'
              OR (t.visibility = 'project' AND EXISTS (
                SELECT 1 FROM project_members pm_subject
                WHERE pm_subject.project_id = t.project_id
                  AND pm_subject.user_id = inv_acl.actor_user_id
                  AND pm_subject.enabled = TRUE
              ))
              OR (t.visibility = 'private' AND (
                t.created_by_user_id = inv_acl.actor_user_id
                OR EXISTS (
                  SELECT 1 FROM task_actors ta_subject
                  WHERE ta_subject.task_id = t.id
                    AND ta_subject.kind = 'user'
                    AND ta_subject.user_id = inv_acl.actor_user_id
                    AND ta_subject.enabled = TRUE
                )
              ))
            ))
          )
        )
        OR (
          ai.audience = 'task'
          AND ai.task_id IS NOT NULL
          AND t.id IS NOT NULL
          AND (
            t.visibility = 'public'
            OR (t.visibility = 'project' AND EXISTS (
              SELECT 1 FROM project_members pm_subject
              WHERE pm_subject.project_id = t.project_id
                AND pm_subject.user_id = inv_acl.actor_user_id
                AND pm_subject.enabled = TRUE
            ))
            OR (t.visibility = 'private' AND (
              t.created_by_user_id = inv_acl.actor_user_id
              OR EXISTS (
                SELECT 1 FROM task_actors ta_subject
                WHERE ta_subject.task_id = t.id
                  AND ta_subject.kind = 'user'
                  AND ta_subject.user_id = inv_acl.actor_user_id
                  AND ta_subject.enabled = TRUE
              )
            ))
          )
        )
        OR (
          ai.audience = 'workspace'
          AND (ai.task_id IS NULL OR (t.id IS NOT NULL AND t.visibility = 'public'))
        )
      )
    )
  )
ORDER BY ai.invoked_at DESC, ai.id DESC
LIMIT ? OFFSET ?;

-- name: ListAiInvocationsForTask :many
-- Recent redacted LLM calls scoped to a single task. Apply the same
-- audience and per-reference ACL policy as the workspace feed. The route
-- already checks access to the subject task, but prompts may quote others.
WITH invocation_policy AS (
  SELECT
    CAST(sqlc.arg('is_elevated') AS SIGNED) AS is_elevated,
    CAST(sqlc.arg('actor_user_id') AS UNSIGNED) AS actor_user_id
)
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
CROSS JOIN invocation_policy inv_acl
LEFT JOIN tasks t ON t.id = ai.task_id AND t.enabled = TRUE
WHERE ai.workspace_id = sqlc.arg('workspace_id')
  AND ai.task_id = sqlc.arg('task_id')
  AND (
    inv_acl.is_elevated = 1
    OR (
      ai.provenance_valid = TRUE
      AND JSON_TYPE(ai.input_resources_json) = 'ARRAY'
      AND ai.input_count = JSON_LENGTH(ai.input_resources_json)
      AND ai.input_count = (
        SELECT COUNT(*)
        FROM ai_invocations_inputs counted_input
        WHERE counted_input.invocation_id = ai.id
      )
      AND NOT EXISTS (
        SELECT 1
        FROM ai_invocations_inputs input_ref
        LEFT JOIN tasks input_task
          ON input_task.workspace_id = ai.workspace_id
          AND input_task.public_id = input_ref.task_public_id
          AND input_task.enabled = TRUE
        WHERE input_ref.invocation_id = ai.id
          AND (
            input_task.id IS NULL
            OR (
              input_task.visibility <> 'public'
              AND NOT (
                input_task.visibility = 'project' AND EXISTS (
                  SELECT 1 FROM project_members pm_input
                  WHERE pm_input.project_id = input_task.project_id
                    AND pm_input.user_id = inv_acl.actor_user_id
                    AND pm_input.enabled = TRUE
                )
              )
              AND (
                input_task.visibility = 'private'
                AND (
                  input_task.created_by_user_id = inv_acl.actor_user_id
                  OR EXISTS (
                    SELECT 1 FROM task_actors ta_input
                    WHERE ta_input.task_id = input_task.id
                      AND ta_input.kind = 'user'
                      AND ta_input.user_id = inv_acl.actor_user_id
                      AND ta_input.enabled = TRUE
                  )
                )
              ) IS NOT TRUE
            )
            OR (ai.audience = 'workspace' AND input_task.visibility <> 'public')
          )
      )
      AND (
        (
          ai.audience = 'initiator'
          AND ai.user_id = inv_acl.actor_user_id
          AND t.id IS NOT NULL
          AND (
            t.visibility = 'public'
            OR (t.visibility = 'project' AND EXISTS (
              SELECT 1 FROM project_members pm_subject
              WHERE pm_subject.project_id = t.project_id
                AND pm_subject.user_id = inv_acl.actor_user_id
                AND pm_subject.enabled = TRUE
            ))
            OR (t.visibility = 'private' AND (
              t.created_by_user_id = inv_acl.actor_user_id
              OR EXISTS (
                SELECT 1 FROM task_actors ta_subject
                WHERE ta_subject.task_id = t.id
                  AND ta_subject.kind = 'user'
                  AND ta_subject.user_id = inv_acl.actor_user_id
                  AND ta_subject.enabled = TRUE
              )
            ))
          )
        )
        OR (
          ai.audience = 'task'
          AND t.id IS NOT NULL
          AND (
            t.visibility = 'public'
            OR (t.visibility = 'project' AND EXISTS (
              SELECT 1 FROM project_members pm_subject
              WHERE pm_subject.project_id = t.project_id
                AND pm_subject.user_id = inv_acl.actor_user_id
                AND pm_subject.enabled = TRUE
            ))
            OR (t.visibility = 'private' AND (
              t.created_by_user_id = inv_acl.actor_user_id
              OR EXISTS (
                SELECT 1 FROM task_actors ta_subject
                WHERE ta_subject.task_id = t.id
                  AND ta_subject.kind = 'user'
                  AND ta_subject.user_id = inv_acl.actor_user_id
                  AND ta_subject.enabled = TRUE
              )
            ))
          )
        )
        OR (
          ai.audience = 'workspace'
          AND t.id IS NOT NULL
          AND t.visibility = 'public'
        )
      )
    )
  )
ORDER BY ai.invoked_at DESC, ai.id DESC
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
