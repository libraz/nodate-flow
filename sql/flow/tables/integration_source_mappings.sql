-- ====================================
-- integration_source_mappings
-- Routes an inbound webhook delivery to the workspace that owns the
-- external source it came from. Every /webhooks/* receiver identifies
-- its sender (GitHub repository, Slack team, Google push channel) and
-- looks the sender up here; without a row the delivery has no tenant
-- and is rejected rather than routed to an arbitrary workspace.
--
-- (provider, external_key) is UNIQUE across the whole instance, not per
-- workspace: one external source belongs to exactly one tenant, so the
-- routing decision can never be ambiguous. `enabled = FALSE` pauses
-- routing while keeping the claim; releasing the source for another
-- workspace requires deleting the row.
-- ====================================
CREATE TABLE integration_source_mappings (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id — the tenant inbound deliveries from this source are routed to',

  provider ENUM('github','slack','google') NOT NULL COMMENT 'Which /webhooks/* receiver this mapping routes',
  external_key VARCHAR(255) CHARACTER SET latin1 COLLATE latin1_bin NOT NULL COMMENT 'Provider-side sender identity, matched byte-for-byte against the delivery: github = repository.id as decimal digits, slack = team_id, google = X-Goog-Channel-ID',
  label VARCHAR(255) NOT NULL COMMENT 'Display-only name for the source (e.g. GitHub owner/repo, Slack workspace name, watched Drive folder)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag — FALSE pauses routing without releasing the (provider, external_key) claim',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_integration_source_mappings_public_id (public_id),
  UNIQUE KEY uniq_integration_source_mappings_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_integration_source_mappings_provider_key (provider, external_key),
  KEY idx_integration_source_mappings_workspace_provider (workspace_id, provider, enabled),

  CONSTRAINT fk_integration_source_mappings_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Maps an external webhook sender to the workspace that owns it';
