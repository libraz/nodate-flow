-- ====================================
-- intake_items
-- Intake triage queue. Items arrive from signals or manual entry,
-- scored by AI, and triaged into tasks or discarded.
-- ====================================
CREATE TABLE intake_items (
  id                  INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id           BINARY(16) NOT NULL                    COMMENT 'UUID v7, the only externally visible ID',
  workspace_id        INT UNSIGNED NOT NULL                   COMMENT 'Internal FK to workspaces.id',
  signal_id           INT UNSIGNED NULL                       COMMENT 'Origin signal (NULL = manual)',
  task_id             INT UNSIGNED NULL                       COMMENT 'Converted task (NULL = not yet converted)',
  triaged_by_user_id  INT UNSIGNED NULL                       COMMENT 'User who triaged this item',

  title               VARCHAR(500) NOT NULL                   COMMENT 'Item title',
  body                MEDIUMTEXT NULL                         COMMENT 'Item body / details',
  triage_status       ENUM('pending','accepted','rejected','snoozed','duplicate') NOT NULL DEFAULT 'pending' COMMENT 'Current triage state',
  snooze_until        DATETIME(3) NULL                        COMMENT 'Snooze expiry (NULL = not snoozed)',
  ai_score            DECIMAL(3,2) NULL                       COMMENT '0.00-1.00',
  ai_reasoning        TEXT NULL                               COMMENT 'AI reasoning for the score',
  scored_at           DATETIME(3) NULL                        COMMENT 'When AI scoring was performed',

  sort_weight         INT NOT NULL DEFAULT 0                  COMMENT 'Display order',
  notes               TEXT NULL                               COMMENT 'Admin notes',
  enabled             BOOLEAN NOT NULL DEFAULT TRUE           COMMENT 'Enabled flag',
  updated_at          TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_intake_items_public_id (public_id),
  UNIQUE KEY uniq_intake_items_workspace_public_id (workspace_id, public_id),
  KEY idx_intake_items_workspace_id_status (workspace_id, triage_status),
  KEY idx_intake_items_workspace_id_snooze (workspace_id, snooze_until),
  KEY idx_intake_items_signal_id (signal_id),

  CONSTRAINT fk_intake_items_workspace  FOREIGN KEY (workspace_id)       REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_intake_items_signal     FOREIGN KEY (signal_id)          REFERENCES signals(id)    ON DELETE SET NULL,
  CONSTRAINT fk_intake_items_task       FOREIGN KEY (task_id)            REFERENCES tasks(id)      ON DELETE SET NULL,
  CONSTRAINT fk_intake_items_triaged_by FOREIGN KEY (triaged_by_user_id) REFERENCES users(id)      ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Intake triage queue';
