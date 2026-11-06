-- ====================================
-- ai_suggestion_decisions
-- Actor-scoped edits, dismissals and deferrals for persisted AI suggestion
-- proposals. Shared execution resolution remains in the event log.
-- ====================================
CREATE TABLE ai_suggestion_decisions (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible decision id',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  proposal_event_id BIGINT UNSIGNED NOT NULL COMMENT 'Internal FK to the proposed event row',
  user_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to the user who owns this decision',

  revision INT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'Actor-scoped decision revision',
  state ENUM('edited','dismissed','deferred') NOT NULL COMMENT 'Actor-scoped decision state',
  action ENUM('archive','snooze','open') NULL COMMENT 'Edited recommendation; null for dismiss or defer',
  reasoning TEXT NULL COMMENT 'Edited recommendation explanation',
  source_version BINARY(32) NULL COMMENT 'SHA-256 version of the source evidence reviewed by the actor',
  expires_at DATETIME(3) NOT NULL COMMENT 'When this actor-scoped decision stops affecting the list',

  updated_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3) COMMENT 'When this decision was last changed',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) COMMENT 'When this decision was first recorded',

  UNIQUE KEY uniq_ai_suggestion_decisions_public_id (public_id),
  UNIQUE KEY uniq_ai_suggestion_decisions_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_ai_suggestion_decisions_proposal_user (workspace_id, proposal_event_id, user_id),
  KEY idx_ai_suggestion_decisions_expiry (workspace_id, user_id, expires_at),

  CONSTRAINT fk_ai_suggestion_decisions_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_ai_suggestion_decisions_proposal FOREIGN KEY (proposal_event_id) REFERENCES events(id) ON DELETE CASCADE,
  CONSTRAINT fk_ai_suggestion_decisions_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT chk_ai_suggestion_decisions_action CHECK (
    (state = 'edited' AND action IS NOT NULL AND reasoning IS NOT NULL AND CHAR_LENGTH(TRIM(reasoning)) > 0)
    OR (state IN ('dismissed','deferred') AND action IS NULL)
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Actor-scoped AI suggestion decisions';
