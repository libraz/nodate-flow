-- ====================================
-- agent_runs
-- Queue + history for AI agent executions (scheduler/worker
-- split). The scheduler enqueues one row per due (agent, scheduled_at)
-- pair with a UNIQUE dedupe_key so multiple scheduler replicas cannot
-- double-fire a job. Workers claim rows with SELECT ... FOR UPDATE
-- SKIP LOCKED, run the agent, then update status to succeeded / failed.
-- Completed rows are retained as the audit trail for
-- `ai.agent.run.*` events.
-- ====================================
CREATE TABLE agent_runs (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  agent_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to ai_agents.id',
  -- Match events.id's unsigned BIGINT range; zero-source jobs store NULL.
  source_event_id BIGINT UNSIGNED NULL COMMENT 'Internal FK to the event that triggered an on_event run',

  dedupe_key VARCHAR(128) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Unique scheduler identity: agent/time for scheduled runs and agent/event for on_event runs',
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
  UNIQUE KEY uniq_agent_runs_agent_source_event (agent_id, source_event_id),
  KEY idx_agent_runs_source_event_id (source_event_id),
  KEY idx_agent_runs_status_scheduled_at (status, scheduled_at),
  KEY idx_agent_runs_workspace_id_agent_id (workspace_id, agent_id),

  CONSTRAINT fk_agent_runs_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_agent_runs_agent FOREIGN KEY (agent_id) REFERENCES ai_agents(id) ON DELETE CASCADE,
  CONSTRAINT fk_agent_runs_source_event FOREIGN KEY (source_event_id) REFERENCES events(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Agent execution queue + history';
