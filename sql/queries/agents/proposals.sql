-- name: InsertAgentActionProposal :execlastid
-- Record a task operation an agent chose but did not apply. status defaults to
-- 'pending'; (run_key, step) is unique so a retried step cannot double-insert.
INSERT INTO agent_action_proposals (
  public_id,
  workspace_id,
  task_id,
  agent_id,
  delegated_by_user_id,
  trigger_user_id,
  run_key,
  step,
  kind,
  args_json,
  precondition_json,
  expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAgentActionProposalForUpdate :one
-- Lock one proposal of a task by its public id, with the proposing agent's
-- current state. The agent's enabled and paused flags are projected rather
-- than filtered: a deleted agent must reach the caller so the proposal can be
-- marked stale instead of vanishing.
SELECT
  p.id,
  p.public_id,
  p.task_id,
  p.agent_id,
  p.delegated_by_user_id,
  p.trigger_user_id,
  p.run_key,
  p.step,
  p.kind,
  p.args_json,
  p.precondition_json,
  p.status,
  p.decided_by_user_id,
  p.decided_at,
  p.decision_reason,
  p.error_code,
  p.expires_at,
  p.created_at,
  a.public_id AS agent_public_id,
  a.name      AS agent_name,
  a.enabled   AS agent_enabled,
  a.paused    AS agent_paused
FROM agent_action_proposals p
INNER JOIN ai_agents a ON a.id = p.agent_id
WHERE p.workspace_id = sqlc.arg('workspace_id')
  AND p.task_id = sqlc.arg('task_id')
  AND p.public_id = sqlc.arg('public_id')
  AND p.enabled = TRUE
LIMIT 1
FOR UPDATE;

-- name: UpdateAgentActionProposalStatus :execrows
-- Move a pending proposal to a terminal status. The status = 'pending' guard
-- makes this a claim: 0 rows means another decision already won.
UPDATE agent_action_proposals
SET status = sqlc.arg('status'),
    decided_by_user_id = sqlc.arg('decided_by_user_id'),
    decided_at = sqlc.arg('decided_at'),
    decision_reason = sqlc.arg('decision_reason'),
    error_code = sqlc.arg('error_code')
WHERE workspace_id = sqlc.arg('workspace_id')
  AND id = sqlc.arg('id')
  AND status = 'pending'
  AND enabled = TRUE;

-- name: CountPendingProposalsForTaskAgent :one
-- Live pending proposals of one agent on one task (expired ones do not count).
SELECT COUNT(*) AS pending_count
FROM agent_action_proposals
WHERE workspace_id = sqlc.arg('workspace_id')
  AND task_id = sqlc.arg('task_id')
  AND agent_id = sqlc.arg('agent_id')
  AND status = 'pending'
  AND expires_at > sqlc.arg('now')
  AND enabled = TRUE;

-- name: FindPendingDuplicateProposal :one
-- A live pending proposal with the same kind and normalized args. JSON equality
-- is key-order independent.
SELECT public_id
FROM agent_action_proposals
WHERE workspace_id = sqlc.arg('workspace_id')
  AND task_id = sqlc.arg('task_id')
  AND agent_id = sqlc.arg('agent_id')
  AND status = 'pending'
  AND expires_at > sqlc.arg('now')
  AND kind = sqlc.arg('kind')
  AND args_json = CAST(sqlc.arg('args_json') AS JSON)
  AND enabled = TRUE
ORDER BY id ASC
LIMIT 1;

-- name: ListRecentRejectedProposals :many
-- The newest rejected proposals of one agent on one task, with the reason the
-- human gave; shown to the agent on its next run.
SELECT
  public_id,
  kind,
  args_json,
  decision_reason,
  decided_at
FROM agent_action_proposals
WHERE workspace_id = sqlc.arg('workspace_id')
  AND task_id = sqlc.arg('task_id')
  AND agent_id = sqlc.arg('agent_id')
  AND status = 'rejected'
  AND enabled = TRUE
ORDER BY decided_at DESC, id DESC
LIMIT ?;

-- name: ListPendingProposalsForTaskAgent :many
-- Live pending proposals of one agent on one task, oldest first.
SELECT
  public_id,
  kind,
  args_json,
  created_at,
  expires_at
FROM agent_action_proposals
WHERE workspace_id = sqlc.arg('workspace_id')
  AND task_id = sqlc.arg('task_id')
  AND agent_id = sqlc.arg('agent_id')
  AND status = 'pending'
  AND expires_at > sqlc.arg('now')
  AND enabled = TRUE
ORDER BY created_at ASC, id ASC
LIMIT ?;

-- name: ListProposalsForTask :many
-- Proposals of every agent on a task, newest first. A NULL status returns all
-- statuses. user_public_id / decided_by_display_name come from a LEFT JOIN and
-- are the decider (zero UUID / NULL while undecided or if the user is gone).
SELECT
  p.public_id,
  p.kind,
  p.args_json,
  p.precondition_json,
  p.status,
  p.decision_reason,
  p.error_code,
  p.decided_at,
  p.expires_at,
  p.created_at,
  a.public_id    AS agent_public_id,
  a.name         AS agent_name,
  du.public_id   AS user_public_id,
  du.display_name AS decided_by_display_name,
  COUNT(*) OVER() AS total
FROM agent_action_proposals p
INNER JOIN ai_agents a ON a.id = p.agent_id
LEFT JOIN users du ON du.id = p.decided_by_user_id
WHERE p.workspace_id = sqlc.arg('workspace_id')
  AND p.task_id = sqlc.arg('task_id')
  AND p.enabled = TRUE
  AND (sqlc.narg('status') IS NULL OR p.status = sqlc.narg('status'))
ORDER BY p.created_at DESC, p.id DESC
LIMIT ?;
