-- name: CreateOrLockOperationReceipt :exec
-- Reserve the operation key inside the caller's transaction. A concurrent
-- insert for the same unique key blocks until its owner commits or rolls
-- back; the following SELECT retrieves and locks the durable receipt.
INSERT INTO operation_receipts (
  public_id,
  workspace_id,
  principal_kind,
  principal_id,
  operation_kind,
  operation_key,
  request_hash,
  target_kind,
  target_public_id,
  result_json,
  completed_at
) VALUES (
  sqlc.arg('public_id'),
  sqlc.arg('workspace_id'),
  sqlc.arg('principal_kind'),
  sqlc.arg('principal_id'),
  sqlc.arg('operation_kind'),
  sqlc.arg('operation_key'),
  sqlc.arg('request_hash'),
  sqlc.arg('target_kind'),
  sqlc.arg('target_public_id'),
  NULL,
  NULL
)
ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id);

-- name: GetOperationReceiptForUpdate :one
-- Read the receipt after reserving its key. The caller re-authorizes the
-- current principal and target before comparing the hash or replaying result.
SELECT
  id,
  public_id,
  request_hash,
  COALESCE(CAST(result_json AS CHAR), '') AS result_json,
  result_json IS NOT NULL AS has_result_json,
  completed_at
FROM operation_receipts
WHERE workspace_id = sqlc.arg('workspace_id')
  AND principal_kind = sqlc.arg('principal_kind')
  AND principal_id = sqlc.arg('principal_id')
  AND operation_kind = sqlc.arg('operation_kind')
  AND operation_key = sqlc.arg('operation_key')
LIMIT 1
FOR UPDATE;

-- name: CompleteOperationReceipt :execrows
-- A successful domain effect and its exact response must complete in the
-- same transaction as this update. Uncompleted reservations never commit.
UPDATE operation_receipts
SET result_json = sqlc.arg('result_json'),
    completed_at = NOW(3)
WHERE id = sqlc.arg('id')
  AND completed_at IS NULL;

-- name: GetSuggestionWorkspaceRoleForShare :one
-- Share-lock only the caller's membership row before authorizing a command
-- or returning a receipt: it blocks a concurrent demotion or removal, while
-- leaving the workspaces row free for FK checks of inserts in the workspace.
SELECT wm.role
FROM workspaces w
INNER JOIN workspace_members wm
  ON wm.workspace_id = w.id
 AND wm.user_id = sqlc.arg('user_id')
 AND wm.enabled = TRUE
WHERE w.public_id = sqlc.arg('workspace_public_id')
  AND w.enabled = TRUE
LIMIT 1
FOR SHARE OF wm;

-- name: GetSuggestionSignalForUpdate :one
-- Resolve the raw signal by workspace/public ID, including archived rows.
-- Receipt replay authorization cannot use v_inbox because archived signals
-- leave that view before the retry arrives.
SELECT
  s.id,
  s.public_id,
  s.workspace_id,
  s.task_id,
  s.source,
  s.kind,
  s.payload_json,
  s.received_at,
  s.updated_at,
  s.enabled
FROM signals s
WHERE s.workspace_id = sqlc.arg('workspace_id')
  AND s.public_id = sqlc.arg('signal_public_id')
LIMIT 1
FOR UPDATE;

-- name: GetSuggestionTaskForUpdate :one
-- task-visibility: not-applicable — this internal lock projection is consumed only by lockAuthorizedTarget, which locks and verifies the current user grant in taskReadable and drops an unreadable row before exposing it; title is used only after that check to validate proposal evidence.
-- Lock the current linked task before checking its visibility or returning
-- a receipt. An absent/disabled task cannot be opened or used to reveal text.
SELECT
  t.id,
  t.workspace_id,
  t.public_id,
  t.title,
  t.project_id,
  t.visibility,
  t.created_by_user_id
FROM tasks t
WHERE t.workspace_id = sqlc.arg('workspace_id')
  AND t.id = sqlc.arg('task_id')
  AND t.enabled = TRUE
LIMIT 1
FOR UPDATE;

-- name: GetSuggestionProjectMemberForUpdate :one
-- Lock a current project-membership grant while checking a linked task.
SELECT pm.id
FROM project_members pm
WHERE pm.project_id = sqlc.arg('project_id')
  AND pm.user_id = sqlc.arg('user_id')
  AND pm.enabled = TRUE
LIMIT 1
FOR UPDATE;

-- name: GetSuggestionTaskActorForUpdate :one
-- Lock a current private-task actor grant while checking a linked task.
SELECT ta.id
FROM task_actors ta
WHERE ta.task_id = sqlc.arg('task_id')
  AND ta.kind = 'user'
  AND ta.user_id = sqlc.arg('user_id')
  AND ta.enabled = TRUE
LIMIT 1
FOR UPDATE;

-- name: FindSuggestionProposalByPublicID :one
-- Resolve an explicit proposal id only when it belongs to the locked signal.
SELECT
  e.id,
  e.public_id,
  e.workspace_id,
  e.occurred_at,
  e.payload_json
FROM events e
INNER JOIN v_ai_suggestion_timeline_events privacy
  ON privacy.event_id = e.id
 AND privacy.owner_user_id = CAST(sqlc.arg('actor_user_id') AS SIGNED)
INNER JOIN v_ai_suggestion_readable_batches readable_batch
  ON readable_batch.id = privacy.batch_id
 AND readable_batch.workspace_id = privacy.workspace_id
 AND readable_batch.user_id = privacy.owner_user_id
WHERE e.workspace_id = sqlc.arg('workspace_id')
  AND e.public_id = sqlc.arg('proposal_public_id')
  AND e.type = 'ai.suggestion.proposed'
  AND COALESCE(
    JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.inbox_item_id')),
    JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.inboxItemId'))
  ) = sqlc.arg('inbox_item_id')
LIMIT 1;

-- name: FindLatestSuggestionProposalForSignal :one
-- Compatibility path for old clients that omitted proposalId. New clients
-- always identify the proposal and its decision revision explicitly.
SELECT
  e.id,
  e.public_id,
  e.workspace_id,
  e.occurred_at,
  e.payload_json
FROM events e
INNER JOIN v_ai_suggestion_timeline_events privacy
  ON privacy.event_id = e.id
 AND privacy.owner_user_id = CAST(sqlc.arg('actor_user_id') AS SIGNED)
INNER JOIN v_ai_suggestion_readable_batches readable_batch
  ON readable_batch.id = privacy.batch_id
 AND readable_batch.workspace_id = privacy.workspace_id
 AND readable_batch.user_id = privacy.owner_user_id
WHERE e.workspace_id = sqlc.arg('workspace_id')
  AND e.type = 'ai.suggestion.proposed'
  AND COALESCE(
    JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.inbox_item_id')),
    JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.inboxItemId'))
  ) = sqlc.arg('inbox_item_id')
ORDER BY e.id DESC
LIMIT 1;

-- name: HasSuggestionProposalReaction :one
-- An applied command changes the shared signal, so it resolves every actor's
-- proposal for that inbox item. A dismissal is the actor's expiring decision
-- row; its event records the outcome and is not terminal.
SELECT EXISTS (
  SELECT 1
  FROM events e
  WHERE e.workspace_id = sqlc.arg('workspace_id')
    AND e.id > sqlc.arg('proposal_event_id')
    AND e.type = 'ai.suggestion.applied'
    AND (
      COALESCE(
        JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.inbox_item_id')),
        JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.inboxItemId'))
      ) = sqlc.arg('inbox_item_id')
      OR COALESCE(
        JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.proposal_id')),
        JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.proposalId'))
      ) = sqlc.arg('proposal_public_id')
    )
) AS has_reaction;

-- name: GetSuggestionDecisionForUpdate :one
-- Read and lock the actor-specific decision for an explicit proposal.
SELECT
  id,
  public_id,
  workspace_id,
  proposal_event_id,
  user_id,
  revision,
  state,
  action,
  reasoning,
  source_version,
  expires_at
FROM ai_suggestion_decisions
WHERE workspace_id = sqlc.arg('workspace_id')
  AND proposal_event_id = sqlc.arg('proposal_event_id')
  AND user_id = sqlc.arg('user_id')
LIMIT 1
FOR UPDATE;

-- name: CreateSuggestionDecision :exec
-- Create the first actor-scoped decision revision. The signal lock serializes
-- edits to the same proposal before this unique key is written.
INSERT INTO ai_suggestion_decisions (
  public_id,
  workspace_id,
  proposal_event_id,
  user_id,
  revision,
  state,
  action,
  reasoning,
  source_version,
  expires_at
) VALUES (
  sqlc.arg('public_id'),
  sqlc.arg('workspace_id'),
  sqlc.arg('proposal_event_id'),
  sqlc.arg('user_id'),
  1,
  sqlc.arg('state'),
  sqlc.narg('action'),
  sqlc.narg('reasoning'),
  sqlc.narg('source_version'),
  sqlc.arg('expires_at')
);

-- name: UpdateSuggestionDecision :execrows
-- Advance the actor's decision revision while the signal and decision rows
-- are locked by the caller.
UPDATE ai_suggestion_decisions
SET revision = revision + 1,
    state = sqlc.arg('state'),
    action = sqlc.narg('action'),
    reasoning = sqlc.narg('reasoning'),
    source_version = sqlc.narg('source_version'),
    expires_at = sqlc.arg('expires_at')
WHERE id = sqlc.arg('id')
  AND workspace_id = sqlc.arg('workspace_id')
  AND user_id = sqlc.arg('user_id');

-- name: ListEffectivePendingAiSuggestionsForReader :many
-- Return the latest globally pending proposal per signal, overlaid with the
-- caller's unexpired edit. Actor-scoped dismissals/deferments hide only that
-- actor's row and never enter the global terminal-reaction predicate.
WITH proposal_candidates AS (
  SELECT
    e.id,
    e.public_id,
    e.workspace_id,
    e.occurred_at,
    e.payload_json,
    COALESCE(
      JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.inbox_item_id')),
      JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.inboxItemId'))
    ) AS inbox_item_id,
    ROW_NUMBER() OVER (
      PARTITION BY e.workspace_id, COALESCE(
        JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.inbox_item_id')),
        JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.inboxItemId'))
      )
      ORDER BY e.id DESC
    ) AS proposal_rank
  FROM events e
  INNER JOIN v_ai_suggestion_timeline_events privacy
    ON privacy.event_id = e.id
   AND privacy.owner_user_id = CAST(sqlc.arg('actor_user_id') AS SIGNED)
  INNER JOIN v_ai_suggestion_readable_batches readable_batch
    ON readable_batch.id = privacy.batch_id
   AND readable_batch.workspace_id = privacy.workspace_id
   AND readable_batch.user_id = privacy.owner_user_id
  WHERE e.workspace_id = sqlc.arg('workspace_id')
    AND e.type = 'ai.suggestion.proposed'
)
SELECT
  p.id AS proposal_event_id,
  p.public_id AS proposal_public_id,
  p.occurred_at,
  p.payload_json,
  v.public_id AS inbox_item_public_id,
  d.revision,
  d.state AS decision_state,
  d.action AS decision_action,
  d.reasoning AS decision_reasoning,
  d.expires_at AS decision_expires_at,
  COALESCE(
    JSON_UNQUOTE(JSON_EXTRACT(p.payload_json, '$.source_version')),
    JSON_UNQUOTE(JSON_EXTRACT(p.payload_json, '$.sourceVersion'))
  ) AS source_version
FROM proposal_candidates p
INNER JOIN v_inbox v
  ON v.workspace_id = p.workspace_id
 AND v.public_id = UUID_TO_BIN(p.inbox_item_id, 0)
LEFT JOIN ai_suggestion_decisions d
  ON d.workspace_id = p.workspace_id
 AND d.proposal_event_id = p.id
 AND d.user_id = CAST(sqlc.arg('actor_user_id') AS SIGNED)
WHERE p.proposal_rank = 1
  -- An unreadable or disabled linked task counts as no task, as in the triage
  -- snapshot; open then has no target, so only signal actions are listed.
  AND (
    COALESCE(
      IF(d.state = 'edited' AND d.expires_at > NOW(3), d.action, NULL),
      JSON_UNQUOTE(JSON_EXTRACT(p.payload_json, '$.action')),
      JSON_UNQUOTE(JSON_EXTRACT(p.payload_json, '$.recommendedAction'))
    ) <> 'open'
    OR (v.task_internal_id IS NOT NULL AND (
      CAST(sqlc.arg('is_elevated') AS SIGNED) = 1
      OR v.task_visibility = 'public'
      OR (v.task_visibility = 'project' AND EXISTS (
        SELECT 1 FROM project_members pm_vis
        WHERE pm_vis.project_id = v.task_project_id
          AND pm_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
          AND pm_vis.enabled = TRUE
      ))
      OR (v.task_visibility = 'private' AND (
        v.task_created_by_user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
        OR EXISTS (
          SELECT 1 FROM task_actors ta_vis
          WHERE ta_vis.task_id = v.task_internal_id
            AND ta_vis.kind = 'user'
            AND ta_vis.user_id = CAST(sqlc.arg('actor_user_id') AS UNSIGNED)
            AND ta_vis.enabled = TRUE
        )
      ))
    ))
  )
  AND NOT EXISTS (
    SELECT 1
    FROM events e2
    WHERE e2.workspace_id = p.workspace_id
      AND e2.id > p.id
      AND e2.type = 'ai.suggestion.applied'
      AND (
        COALESCE(
          JSON_UNQUOTE(JSON_EXTRACT(e2.payload_json, '$.inbox_item_id')),
          JSON_UNQUOTE(JSON_EXTRACT(e2.payload_json, '$.inboxItemId'))
        ) = p.inbox_item_id
        OR COALESCE(
          JSON_UNQUOTE(JSON_EXTRACT(e2.payload_json, '$.proposal_id')),
          JSON_UNQUOTE(JSON_EXTRACT(e2.payload_json, '$.proposalId'))
        ) = BIN_TO_UUID(p.public_id, 0)
      )
  )
  AND (d.id IS NULL OR d.expires_at <= NOW(3) OR d.state NOT IN ('dismissed','deferred'))
ORDER BY p.occurred_at DESC, p.id DESC
LIMIT 100;
