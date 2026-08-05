-- ====================================
-- repo_workspace_mappings
-- Maps a GitHub repository to a workspace so incoming webhooks can
-- be routed to the correct tenant. Optionally pins a default project
-- and controls which event types are synchronised as tasks.
-- ====================================
CREATE TABLE repo_workspace_mappings (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  integration_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to user_integrations.id (the GitHub OAuth connection)',

  repo_full_name VARCHAR(255) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'GitHub owner/repo (e.g. libraz/nodate-flow)',
  repo_id BIGINT UNSIGNED NOT NULL COMMENT 'GitHub numeric repository ID for webhook lookup',
  default_project_id INT UNSIGNED NULL COMMENT 'Optional FK to projects.id for routing issues/PRs',
  is_sync_issues BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Sync GitHub issues as tasks',
  is_sync_pull_requests BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Sync GitHub pull requests as tasks',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_repo_workspace_mappings_public_id (public_id),
  UNIQUE KEY uniq_repo_workspace_mappings_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_repo_workspace_mappings_workspace_repo (workspace_id, repo_full_name),
  KEY idx_repo_workspace_mappings_repo_id (repo_id),
  KEY idx_repo_workspace_mappings_integration_id (integration_id),
  KEY idx_repo_workspace_mappings_workspace_id_enabled (workspace_id, enabled),

  CONSTRAINT fk_repo_workspace_mappings_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_repo_workspace_mappings_integration FOREIGN KEY (integration_id) REFERENCES user_integrations(id) ON DELETE CASCADE,
  CONSTRAINT fk_repo_workspace_mappings_project FOREIGN KEY (default_project_id) REFERENCES projects(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Maps GitHub repositories to workspaces for webhook routing';
