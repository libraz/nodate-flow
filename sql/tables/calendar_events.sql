-- ====================================
-- calendar_events
-- Calendar event with structured kinds (event/block/free), visibility
-- levels, and show_as states. owner_user_id determines whose layer the
-- event appears on; created_by tracks who actually created it (supports
-- manager delegation).
-- ====================================
CREATE TABLE calendar_events (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  calendar_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to calendars.id',

  -- Event classification
  kind ENUM('event','block','free') NOT NULL DEFAULT 'event' COMMENT 'event=regular, block=declarative time frame (work hours, focus), free=available slot',
  visibility ENUM('default','public','private','confidential') NOT NULL DEFAULT 'default' COMMENT 'Who can see event details: default (calendar setting), public (all), private (time only), confidential (owner only)',
  show_as ENUM('busy','free','tentative','oof') NOT NULL DEFAULT 'busy' COMMENT 'Availability display: busy, free, tentative, out-of-office',

  title VARCHAR(500) NOT NULL COMMENT 'Event title',
  all_day BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'All-day event flag',
  start_at DATETIME NOT NULL COMMENT 'Start time (UTC or with timezone context)',
  end_at DATETIME NOT NULL COMMENT 'End time',
  timezone VARCHAR(64) CHARACTER SET latin1 COLLATE latin1_swedish_ci NOT NULL DEFAULT 'Asia/Tokyo' COMMENT 'IANA timezone identifier',

  location VARCHAR(500) NULL COMMENT 'Location text',
  memo MEDIUMTEXT NULL COMMENT 'Free-form notes (markdown)',
  url VARCHAR(2000) CHARACTER SET latin1 COLLATE latin1_swedish_ci NULL COMMENT 'Meeting link or related URL',

  -- Ownership: determines whose layer this event belongs to
  owner_user_id INT UNSIGNED NOT NULL COMMENT 'Event owner (whose color/layer). Only owner, managers, or can_edit attendees may edit',
  created_by_user_id INT UNSIGNED NOT NULL COMMENT 'Actual creator (may differ from owner for manager delegation)',

  -- Block metadata
  block_label VARCHAR(100) NULL COMMENT 'Label for block-kind events (e.g., Working, Focus Time, Out of Office)',

  -- Recurrence (RFC 5545 subset stored as JSON)
  recurrence_rule JSON NULL COMMENT 'Recurrence rule: {freq, interval, byDay, byMonthDay, bySetPos, until, count}',
  recurrence_end DATETIME NULL COMMENT 'Computed end date for recurrence expansion queries',

  notification_offset INT NULL COMMENT 'Minutes before event to send notification; NULL = no notification',

  -- Cross-module link to nodate-flow tasks
  task_id INT UNSIGNED NULL COMMENT 'Linked task (optional, for task-calendar sync)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_calendar_events_public_id (public_id),
  KEY idx_calendar_events_calendar_range (calendar_id, start_at, end_at),
  KEY idx_calendar_events_workspace_owner (workspace_id, owner_user_id, start_at),
  KEY idx_calendar_events_calendar_recurrence (calendar_id, recurrence_end),
  KEY idx_calendar_events_task (task_id),
  FULLTEXT KEY ft_calendar_events_title_memo (title, memo),

  CONSTRAINT fk_calendar_events_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_calendar_events_calendar FOREIGN KEY (calendar_id) REFERENCES calendars(id) ON DELETE CASCADE,
  CONSTRAINT fk_calendar_events_owner FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_calendar_events_creator FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_calendar_events_task FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Calendar events with kind/visibility/show_as classification';
