-- Agent task actions: per-operation grants, approval proposals, delegator.
--
-- 1. task_actors gains delegated_by_user_id (FK users, ON DELETE SET NULL).
-- 2. agent_action_proposals is created.
-- 3. delegated_by_user_id is backfilled for enabled agent actor rows that have
--    none, from the latest task.actor.added (same agentId and role) or
--    agent.task.handoff_to_agent (same agentId) event on the task that has a
--    human actor.
-- 4. ai_agents.tools_json of task_agent rows holding SQL NULL, JSON null or a
--    JSON array becomes '{}' (no grants). signal_judge rows are left alone.
--
-- Every step checks its own precondition, so the file can be run again.

SET NAMES utf8mb4;

DROP PROCEDURE IF EXISTS flow_upgrade_0002;

DELIMITER //

CREATE PROCEDURE flow_upgrade_0002()
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'task_actors'
      AND COLUMN_NAME = 'delegated_by_user_id'
  ) THEN
    ALTER TABLE task_actors
      ADD COLUMN delegated_by_user_id INT UNSIGNED NULL
        COMMENT 'Internal FK to users.id: human who assigned this agent (delegator); caps automatic application of the agent''s writes. Null for human actors or when unrecorded'
        AFTER role;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM information_schema.TABLE_CONSTRAINTS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'task_actors'
      AND CONSTRAINT_NAME = 'fk_task_actors_delegated_by'
  ) THEN
    ALTER TABLE task_actors
      ADD CONSTRAINT fk_task_actors_delegated_by
        FOREIGN KEY (delegated_by_user_id) REFERENCES users(id) ON DELETE SET NULL;
  END IF;

  CREATE TABLE IF NOT EXISTS agent_action_proposals (
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

  -- Backfill: only rows without a delegator, so a re-run changes nothing.
  UPDATE task_actors ta
  INNER JOIN ai_agents a ON a.id = ta.agent_id
  SET ta.delegated_by_user_id = (
    SELECT u.id
    FROM events e
    INNER JOIN users u ON u.id = e.actor_user_id
    WHERE e.task_id = ta.task_id
      AND e.workspace_id = ta.workspace_id
      AND e.actor_user_id IS NOT NULL
      AND JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.agentId')) = BIN_TO_UUID(a.public_id, 0)
      AND (
        (e.type = 'task.actor.added'
          AND JSON_UNQUOTE(JSON_EXTRACT(e.payload_json, '$.role')) = ta.role)
        OR e.type = 'agent.task.handoff_to_agent'
      )
    ORDER BY e.id DESC
    LIMIT 1
  )
  WHERE ta.kind = 'agent'
    AND ta.enabled = TRUE
    AND ta.delegated_by_user_id IS NULL;

  -- Existing task agents get no grants until an admin sets them.
  UPDATE ai_agents
  SET tools_json = JSON_OBJECT()
  WHERE kind = 'task_agent'
    AND (tools_json IS NULL OR JSON_TYPE(tools_json) IN ('NULL', 'ARRAY'));
END//

DELIMITER ;

CALL flow_upgrade_0002();

DROP PROCEDURE flow_upgrade_0002;
