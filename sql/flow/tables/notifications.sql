-- ====================================
-- notifications
-- Per-user notification entries produced by eventbus fan-out when workspace
-- events occur. Each row represents one notification to one recipient.
-- ====================================
CREATE TABLE notifications (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  recipient_user_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id, the user who receives this notification',
  actor_user_id INT UNSIGNED NULL COMMENT 'Internal FK to users.id, who triggered the event (null for system)',
  -- BIGINT UNSIGNED to match events.id, which is a BIGINT UNSIGNED exception
  -- (append-only log expected to grow past the 4.29B INT UNSIGNED ceiling).
  source_event_id BIGINT UNSIGNED NULL COMMENT 'Internal FK to events.id (BIGINT UNSIGNED) used for at-least-once dedup; null for non-event-driven paths (scheduler, system)',

  event_type VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Matches eventbus event types (e.g. task.created, task.comment.added)',
  resource_type VARCHAR(32) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL COMMENT 'Resource kind: task, project, comment, etc.',
  resource_public_id BINARY(16) NULL COMMENT 'public_id of the affected resource (null for workspace-level events)',
  title VARCHAR(255) NOT NULL COMMENT 'Human-readable notification title (i18n key or pre-rendered)',
  body TEXT NULL COMMENT 'Optional longer description',
  severity ENUM('low','normal','high','urgent') NOT NULL DEFAULT 'normal' COMMENT 'AI-inferred or rule-based severity',
  channel ENUM('in_app','email','push') NOT NULL DEFAULT 'in_app' COMMENT 'Delivery channel',
  read_at DATETIME(3) NULL COMMENT 'When the user marked it read (null = unread)',
  archived_at DATETIME(3) NULL COMMENT 'When the user archived it (null = active)',
  delivered_at DATETIME(3) NULL COMMENT 'When email/push was actually sent (null for in_app only)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_notifications_public_id (public_id),
  UNIQUE KEY uniq_notifications_workspace_public_id (workspace_id, public_id),
  -- At-least-once dedup: a single (recipient, source_event, channel) tuple
  -- yields exactly one row even if the fan-out goroutine retries, so a
  -- replayed hook cannot show the same thing twice in someone's inbox.
  --
  -- A unique index never treats an entry containing NULL as a duplicate,
  -- and that is the behaviour a row with no source event needs: two
  -- notifications a background process raised for the same recipient are
  -- two different things to say, not one said twice, and there is no
  -- identity to collapse them on. The protection is therefore only as
  -- wide as the writers make it — a caller that has an events row and
  -- omits it opts its notifications out of dedup.
  UNIQUE KEY uniq_notifications_recipient_source_channel (recipient_user_id, source_event_id, channel),
  KEY idx_notifications_workspace_id_recipient_read (workspace_id, recipient_user_id, read_at, created_at DESC),
  KEY idx_notifications_workspace_id_recipient_archived (workspace_id, recipient_user_id, archived_at, created_at DESC),
  KEY idx_notifications_workspace_id_event_type (workspace_id, event_type),
  KEY idx_notifications_recipient_unread (recipient_user_id, read_at, archived_at, enabled),
  -- Supports cross-workspace keyset pagination on (created_at DESC, public_id DESC)
  -- for ListNotificationsForUserKeyset (no workspace_id filter).
  KEY idx_notifications_user_id_keyset (recipient_user_id, created_at, public_id),

  CONSTRAINT fk_notifications_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_notifications_recipient FOREIGN KEY (recipient_user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_notifications_actor FOREIGN KEY (actor_user_id) REFERENCES users(id) ON DELETE SET NULL,
  CONSTRAINT fk_notifications_source_event FOREIGN KEY (source_event_id) REFERENCES events(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Per-user notification entries from eventbus fan-out';
