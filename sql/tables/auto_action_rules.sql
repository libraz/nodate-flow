-- ====================================
-- auto_action_rules
-- Per-workspace auto-action rule configuration.
-- Each row overrides the default confidence and idle threshold for a
-- specific rule kind (e.g. escalate_overdue, assign_owner).
-- The auto-action executor reads these rules together with ai_settings
-- to decide which actions to propose or apply automatically.
-- ====================================
CREATE TABLE auto_action_rules (
  id            INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id     BINARY(16)   NOT NULL COMMENT 'UUID v7, used in API responses',
  workspace_id  INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  kind          VARCHAR(64)  NOT NULL COMMENT 'Rule kind: escalate_overdue | assign_owner | nudge_assignee | close_stale_review',
  enabled       BOOLEAN      NOT NULL DEFAULT TRUE COMMENT 'Whether this rule fires during evaluation',
  confidence    DECIMAL(3,2) NOT NULL COMMENT 'Confidence score emitted when this rule fires (0.00-1.00)',
  idle_hours    INT UNSIGNED NOT NULL COMMENT 'Idle threshold in hours. 0 for rules that use due_on (escalate_overdue)',

  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_auto_action_rules_public_id (public_id),
  UNIQUE KEY uniq_auto_action_rules_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_auto_action_rules_ws_kind (workspace_id, kind),

  CONSTRAINT fk_auto_action_rules_ws FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Per-workspace auto-action rule overrides (kind, confidence, idle threshold)';
