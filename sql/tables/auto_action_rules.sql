-- ====================================
-- auto_action_rules
-- Per-workspace auto-action rule configuration.
-- Each row overrides the default confidence and idle threshold for a
-- specific rule kind (e.g. escalate_overdue, assign_owner) and can be
-- further scoped to a specific signal_kind. A NULL signal_kind acts as
-- the wildcard fallback that matches every signal kind for the rule.
-- Rules may also declare an explicit autonomy_level that overrides the
-- confidence-vs-threshold gate, so the autonomy matrix UI can persist a
-- chosen mode (suggest | draft | auto) directly without encoding it via
-- confidence values.
-- The auto-action executor reads these rules together with ai_settings
-- to decide which actions to propose or apply automatically.
-- ====================================
CREATE TABLE auto_action_rules (
  id            INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id     BINARY(16)   NOT NULL COMMENT 'UUID v7, used in API responses',
  workspace_id  INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  kind          VARCHAR(64)  NOT NULL COMMENT 'Rule kind: escalate_overdue | assign_owner | nudge_assignee | close_stale_review',
  signal_kind   VARCHAR(64)  NULL COMMENT 'Signal scope: NULL = wildcard (matches every kind), exact dotted string match (e.g. ''discord.presence''), or wildcard prefix where the stored value matches kinds with that suffix-after-dot. Resolution layer details live in docs/conventions/autonomy.md',
  enabled       BOOLEAN      NOT NULL DEFAULT TRUE COMMENT 'Whether this rule fires during evaluation',
  confidence    DECIMAL(3,2) NOT NULL COMMENT 'Confidence score emitted when this rule fires (0.00-1.00)',
  idle_hours    INT UNSIGNED NOT NULL COMMENT 'Idle threshold in hours. 0 for rules that use due_on (escalate_overdue)',
  autonomy_level ENUM('suggest','draft','auto') NULL
    COMMENT 'When set, the autonomy resolver returns this level verbatim and skips confidence comparison. NULL falls back to the existing confidence-vs-threshold derivation. Closed enum mirrors signalkinds.Autonomy.',

  signal_kind_match VARCHAR(64) GENERATED ALWAYS AS (COALESCE(signal_kind, '')) STORED NOT NULL
    COMMENT 'Internal normalization of signal_kind for the UNIQUE index. Empty string represents the NULL wildcard. Never read from app code -- only the unique-key engine touches this. Constraint order (GENERATED clause before NOT NULL) is required by MySQL 9.x parser.',

  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_auto_action_rules_public_id (public_id),
  UNIQUE KEY uniq_auto_action_rules_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_auto_action_rules_ws_kind_signal (workspace_id, kind, signal_kind_match),

  KEY idx_auto_action_rules_ws_signal (workspace_id, signal_kind, kind),

  CONSTRAINT fk_auto_action_rules_ws FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Per-workspace auto-action rule overrides (kind, signal_kind, confidence, idle threshold)';
