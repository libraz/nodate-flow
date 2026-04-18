-- ====================================
-- calendar_subscriptions
-- A user's relationship to a calendar: membership + display preferences.
-- For shared calendars, member_color is the per-person color visible to
-- all members (TimeTree-style). For personal/system calendars,
-- display_color is the subscriber's private display preference.
-- ====================================
CREATE TABLE calendar_subscriptions (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  calendar_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to calendars.id',
  user_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id',

  role ENUM('owner','manager','editor','viewer') NOT NULL DEFAULT 'editor' COMMENT 'Calendar-level role: owner (full), manager (delegate), editor (own events), viewer (read-only)',

  -- Shared calendar: per-member color seen by everyone in the calendar.
  member_color VARCHAR(7) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL DEFAULT '#4285F4' COMMENT 'Member color in shared calendars (visible to all)',
  -- Personal/system calendar: subscriber-private display color.
  display_color VARCHAR(7) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL DEFAULT '#4285F4' COMMENT 'Display color for personal/system calendars (private to subscriber)',

  visible BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Whether this calendar layer is shown in UI',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order in sidebar',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_calendar_subscriptions_public_id (public_id),
  UNIQUE KEY uniq_calendar_subscriptions_calendar_user (calendar_id, user_id),
  KEY idx_calendar_subscriptions_user_workspace (user_id, workspace_id),
  KEY idx_calendar_subscriptions_workspace_calendar (workspace_id, calendar_id),

  CONSTRAINT fk_calendar_subscriptions_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_calendar_subscriptions_calendar FOREIGN KEY (calendar_id) REFERENCES calendars(id) ON DELETE CASCADE,
  CONSTRAINT fk_calendar_subscriptions_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Calendar membership and display preferences';
