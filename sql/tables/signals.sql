-- ====================================
-- signals
-- External or manually submitted signals (webhooks, manual drops, MCP emits,
-- worker ticks) that may be normalized and routed to the LLM judge for a
-- verdict. Optionally linked to a task via `task_id` (legacy fast path) and/or
-- to an arbitrary subject via `(subject_type, subject_id)`.
--
-- See ADR 0008 (docs/adr/0008-signals-and-judge-loop.md) D1 for the rationale
-- for extending this table in place rather than introducing `signals_v2`,
-- and D5 for how `v_user_presence_current` projects the latest presence row
-- per `(workspace_id, subject_id)` out of this table.
--
-- Lifecycle columns:
--   judge_run_id      - FK to the agent_runs row that judged this signal
--                       (NULL until the judge has evaluated it).
--   judge_output_json - structured verdict from the judge (intent, target
--                       task, proposed events, confidence, reasoning excerpt).
--   confidence        - judge's reported confidence in [0.00, 1.00]; compared
--                       against ai_settings.auto_action_threshold and
--                       auto_action_rules to decide suggest / draft / auto.
--   applied_at        - set by the Applier when the verdict has been reified
--                       as one or more task events; NULL while pending or
--                       rejected.
--   expires_at        - provider-derived TTL; presence signals expire on the
--                       next presence transition, weather signals expire at
--                       the end of the window, manual signals do not expire.
--                       The retention sweep drops expired, non-applied rows
--                       for stateful kinds.
--
-- Subject columns:
--   subject_type - what the signal is about. `task_id` remains the dedicated
--                  optimised path for `subject_type='task'`; for the other
--                  three subjects, `subject_id` carries the internal FK
--                  whose target is named by `subject_type`.
--   subject_id   - internal id of the subject row (NULL when subject_type is
--                  `workspace`, since `workspace_id` already identifies the
--                  row owner). Not declared as an FK because the target table
--                  is polymorphic; integrity is enforced by handler validation
--                  at ingestion time.
-- ====================================
CREATE TABLE signals (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  task_id INT UNSIGNED NULL COMMENT 'Internal FK to tasks.id, if resolved (legacy fast path; duplicates subject_type=task / subject_id)',
  judge_run_id INT UNSIGNED NULL COMMENT 'Internal FK to agent_runs.id; set when a judge run has evaluated this signal. NULL means "not yet judged".',

  source ENUM('manual','github','slack','email','google','webhook','calendar','discord') NOT NULL COMMENT 'Originating channel. ''calendar'' is reserved for internal scheduler ticks (flow-worker calendar_event_day job, etc.) — not a user-facing webhook source. ''discord'' is the presence-discord gateway.',
  kind VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Source-specific event kind (e.g., pull_request.opened, discord.presence). Closed enumeration defined by signal_kinds/*.yaml; stays VARCHAR so new kinds do not require a schema change.',
  external_id VARCHAR(255) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'External identifier (delivery id, message ts, ...). Dedupe key for webhook double delivery.',
  payload_json JSON NOT NULL CHECK (JSON_VALID(payload_json)) COMMENT 'Raw normalized payload; anything the provider webhook cannot squeeze into the normalized columns stays here for the judge to read.',
  received_at DATETIME(3) NOT NULL COMMENT 'Time the signal was received',

  subject_type ENUM('user','task','workspace','calendar_event') NOT NULL COMMENT 'What the signal is about; selects which table subject_id targets.',
  subject_id INT UNSIGNED NULL COMMENT 'Internal id of the subject row. NULL when subject_type=workspace (workspace_id already owns the row). Polymorphic, so not declared as an FK; integrity is enforced at ingestion time.',

  judge_output_json JSON NULL CHECK (judge_output_json IS NULL OR JSON_VALID(judge_output_json)) COMMENT 'Structured verdict from the judge run (intent, target task, proposed events, reasoning excerpt). NULL until judged.',
  confidence DECIMAL(3,2) NULL COMMENT 'Judge confidence in [0.00, 1.00]; compared against ai_settings.auto_action_threshold / auto_action_rules.',
  applied_at DATETIME(3) NULL COMMENT 'When the Applier wrote the resulting task event(s). NULL while pending or rejected.',
  expires_at DATETIME(3) NULL COMMENT 'When this signal stops being authoritative. Used by the retention sweep for stateful kinds (presence, weather window).',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  CONSTRAINT chk_signals_confidence_range CHECK (confidence IS NULL OR (confidence >= 0.00 AND confidence <= 1.00)),

  UNIQUE KEY uniq_signals_public_id (public_id),
  UNIQUE KEY uniq_signals_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_signals_workspace_source_external_id (workspace_id, source, external_id),
  KEY idx_signals_workspace_id_received_at (workspace_id, received_at),
  KEY idx_signals_workspace_id_task_id (workspace_id, task_id),
  -- Supports v_user_presence_current (latest row per subject) and per-subject
  -- judge queue scans. observed_at-style ordering uses received_at; the index
  -- direction is descending so the "latest first" scan is index-only.
  KEY idx_signals_subject (workspace_id, subject_type, subject_id, received_at DESC),
  -- Reverse lookup from agent_runs (operator visibility: "which signal did
  -- this judge run evaluate?").
  KEY idx_signals_judge_run (judge_run_id),
  -- Retention sweep over expired, non-applied rows. MySQL 8.4 has no partial
  -- index support; the index is plain and the sweep query carries the
  -- IS NOT NULL filter.
  KEY idx_signals_expires (expires_at),
  -- Per-workspace, per-kind history (judge prompt context, autonomy metrics).
  KEY idx_signals_workspace_kind_received (workspace_id, kind, received_at),

  CONSTRAINT fk_signals_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_signals_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL,
  CONSTRAINT fk_signals_judge_run FOREIGN KEY (judge_run_id) REFERENCES agent_runs(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Inbound signals; judged by the signal_judge agent and reified into task events by the Applier (ADR 0008)';
