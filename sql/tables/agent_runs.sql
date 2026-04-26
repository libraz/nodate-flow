-- ====================================
-- agent_runs
-- Queue + history for AI agent executions (scheduler/worker
-- split). The scheduler enqueues one row per due (agent, scheduled_at)
-- pair with a UNIQUE dedupe_key so multiple scheduler replicas cannot
-- double-fire a job. Workers claim rows with SELECT ... FOR UPDATE
-- SKIP LOCKED, run the agent, then update status to succeeded / failed.
-- Completed rows are retained as the audit trail for
-- `ai.agent.run.*` events (ADR 0002 — events in MySQL).
-- ====================================
CREATE TABLE agent_runs (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  agent_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to ai_agents.id',

  dedupe_key VARCHAR(128) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Unique key shaped as <agent_id>:<unix_minute> to prevent double enqueue across scheduler replicas',
  status ENUM('pending','claimed','succeeded','failed') NOT NULL DEFAULT 'pending' COMMENT 'Lifecycle state',
  attempts TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Number of claim attempts (for retry budget)',
  scheduled_at DATETIME(3) NOT NULL COMMENT 'Tick time the scheduler enqueued the run for',
  claimed_at DATETIME(3) NULL COMMENT 'When a worker claimed the row',
  finished_at DATETIME(3) NULL COMMENT 'When the worker ack/nacked the row',
  error_message TEXT NULL COMMENT 'Last failure message for operator visibility',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_agent_runs_public_id (public_id),
  UNIQUE KEY uniq_agent_runs_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_agent_runs_dedupe_key (dedupe_key),
  KEY idx_agent_runs_status_scheduled_at (status, scheduled_at),
  KEY idx_agent_runs_workspace_id_agent_id (workspace_id, agent_id),

  CONSTRAINT fk_agent_runs_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_agent_runs_agent FOREIGN KEY (agent_id) REFERENCES ai_agents(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Agent execution queue + history';
