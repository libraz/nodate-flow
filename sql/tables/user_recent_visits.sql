-- ====================================
-- user_recent_visits
-- Per-user recently visited entities. Upserted on each visit;
-- old rows pruned by a background job.
-- ====================================
CREATE TABLE user_recent_visits (
  id               INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id        BINARY(16) NOT NULL                    COMMENT 'UUID v7, the only externally visible ID',
  workspace_id     INT UNSIGNED NOT NULL,
  user_id          INT UNSIGNED NOT NULL,

  entity_type      ENUM('project','task','page','lens','timebox') NOT NULL,
  entity_public_id BINARY(16) NOT NULL,
  entity_title     VARCHAR(500) NULL COMMENT 'Denormalized title snapshot',

  sort_weight      INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes            TEXT NULL COMMENT 'Admin notes',
  enabled          BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at       TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_user_recent_visits_public_id (public_id),
  UNIQUE KEY uniq_user_recent_visits_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_user_recent_visits_user_entity (user_id, entity_type, entity_public_id),
  KEY idx_user_recent_visits_workspace_user_updated (workspace_id, user_id, updated_at DESC),

  CONSTRAINT fk_user_recent_visits_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_user_recent_visits_user      FOREIGN KEY (user_id)      REFERENCES users(id)      ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Per-user recently visited entities';
