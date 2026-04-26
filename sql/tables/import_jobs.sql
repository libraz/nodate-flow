-- ====================================
-- import_jobs
-- Bulk import job tracking. Each row represents a single import
-- operation from an external source (GitHub, Jira, Linear, CSV).
-- The worker updates processed_items / failed_items as it progresses.
-- ====================================
CREATE TABLE import_jobs (
  id                   INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id            BINARY(16) NOT NULL                     COMMENT 'UUID v7, the only externally visible ID',
  workspace_id         INT UNSIGNED NOT NULL                    COMMENT 'Internal FK to workspaces.id',
  project_id           INT UNSIGNED NULL                        COMMENT 'Internal FK to projects.id (target project)',
  initiated_by_user_id INT UNSIGNED NULL                        COMMENT 'Internal FK to users.id (who started the import)',

  source               ENUM('github','jira','linear','csv') NOT NULL COMMENT 'Import source type',
  status               ENUM('pending','running','completed','failed','cancelled') NOT NULL DEFAULT 'pending' COMMENT 'Lifecycle state',
  total_items          INT UNSIGNED NOT NULL DEFAULT 0          COMMENT 'Total items to import',
  processed_items      INT UNSIGNED NOT NULL DEFAULT 0          COMMENT 'Successfully processed items',
  failed_items         INT UNSIGNED NOT NULL DEFAULT 0          COMMENT 'Items that failed to import',
  config_json          JSON NOT NULL                            COMMENT 'Source-specific import configuration',
  error_log            TEXT NULL                                COMMENT 'Aggregated error log',
  started_at           DATETIME(3) NULL                         COMMENT 'When the worker began processing',
  completed_at         DATETIME(3) NULL                         COMMENT 'When the import finished (success or failure)',

  sort_weight          INT NOT NULL DEFAULT 0                   COMMENT 'Display order',
  notes                TEXT NULL                                COMMENT 'Admin notes',
  enabled              BOOLEAN NOT NULL DEFAULT TRUE            COMMENT 'Enabled flag',
  updated_at           TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at           DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_import_jobs_public_id (public_id),
  UNIQUE KEY uniq_import_jobs_workspace_public_id (workspace_id, public_id),
  KEY idx_import_jobs_workspace_id_status (workspace_id, status),

  CONSTRAINT fk_import_jobs_workspace FOREIGN KEY (workspace_id)         REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_import_jobs_project   FOREIGN KEY (project_id)           REFERENCES projects(id)   ON DELETE SET NULL,
  CONSTRAINT fk_import_jobs_initiator FOREIGN KEY (initiated_by_user_id) REFERENCES users(id)      ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Bulk import job tracking';
