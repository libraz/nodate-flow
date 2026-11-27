-- ====================================
-- agent_action_proposals
-- A task operation an AI agent chose but did not apply: the grant is
-- 'approval', or the delegator / trigger user cannot authorize an automatic
-- apply. A human approves or rejects it from the task; approval re-checks the
-- precondition and applies the operation as the agent.
-- ====================================
CREATE TABLE agent_action_proposals (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to tasks.id (the task the operation targets)',
  agent_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to ai_agents.id (the agent that proposed the operation)',
  delegated_by_user_id INT UNSIGNED NULL COMMENT 'Internal FK to users.id: delegator of the agent assignment at proposal time (record only; null when none)',
  trigger_user_id INT UNSIGNED NULL COMMENT 'Internal FK to users.id: human whose event started the run (record only)',

  run_key CHAR(36) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'UUID of the agent run that produced the proposal',
  step SMALLINT UNSIGNED NOT NULL COMMENT 'Step number within the run; (run_key, step) is unique',
  kind ENUM('add_comment','set_priority','set_due_on','transition_task','create_subtask') NOT NULL COMMENT 'Proposed operation; same closed set as the keys of ai_agents.tools_json',
  args_json JSON NOT NULL COMMENT 'Normalized arguments of the operation (only the keys the kind uses)',
  precondition_json JSON NULL COMMENT 'Values compared at approval to detect staleness (priority, due_on, derived_state plus latest transition event id); null when the kind has none',
  status ENUM('pending','applied','rejected','stale','expired','failed') NOT NULL DEFAULT 'pending' COMMENT 'pending: awaiting a decision. applied: approved and written. rejected: declined by a human. stale: the target changed since the proposal. expired: not decided before expires_at. failed: approval was attempted but the write was refused',
  decided_by_user_id INT UNSIGNED NULL COMMENT 'Internal FK to users.id: human who approved or rejected',
  decided_at DATETIME(3) NULL COMMENT 'Time of the decision or of the terminal status change',
  decision_reason VARCHAR(500) NULL COMMENT 'Reason given when rejecting; shown to the agent on its next run',
  error_code VARCHAR(64) NULL COMMENT 'Error code explaining a failed or stale status',
  expires_at DATETIME(3) NOT NULL COMMENT 'Deadline for a decision (creation plus 7 days); stored in UTC',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_agent_action_proposals_public_id (public_id),
  UNIQUE KEY uniq_agent_action_proposals_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_agent_action_proposals_run_key_step (run_key, step),
  KEY idx_agent_action_proposals_task_id_agent_id_status (task_id, agent_id, status),
  KEY idx_agent_action_proposals_agent_id (agent_id),
  KEY idx_agent_action_proposals_delegated_by_user_id (delegated_by_user_id),
  KEY idx_agent_action_proposals_trigger_user_id (trigger_user_id),
  KEY idx_agent_action_proposals_decided_by_user_id (decided_by_user_id),

  CONSTRAINT fk_agent_action_proposals_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_agent_action_proposals_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  CONSTRAINT fk_agent_action_proposals_agent FOREIGN KEY (agent_id) REFERENCES ai_agents(id) ON DELETE CASCADE,
  CONSTRAINT fk_agent_action_proposals_delegated_by FOREIGN KEY (delegated_by_user_id) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_agent_action_proposals_trigger_user FOREIGN KEY (trigger_user_id) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_agent_action_proposals_decided_by FOREIGN KEY (decided_by_user_id) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Agent-proposed task operations awaiting human approval';
