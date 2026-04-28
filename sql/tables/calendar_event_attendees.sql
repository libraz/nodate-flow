-- ====================================
-- calendar_event_attendees
-- Event participants with RSVP state and per-attendee edit permission.
-- The event owner can grant can_edit to specific attendees, enabling
-- collaborative editing without giving full calendar manager access.
-- ====================================
CREATE TABLE calendar_event_attendees (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  public_id BINARY(16) NOT NULL COMMENT 'UUID v7, the only externally visible ID',
  workspace_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to workspaces.id',
  event_id INT UNSIGNED NULL COMMENT 'Internal FK to calendar_events.id; nullable so audit-trail attendee rows survive event hard-delete (FK SET NULL); active rows for live events are NOT NULL via app constraint',
  user_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id',

  rsvp ENUM('pending','accepted','declined','tentative') NOT NULL DEFAULT 'pending' COMMENT 'Attendance response',
  can_edit BOOLEAN NOT NULL DEFAULT FALSE COMMENT 'Whether this attendee can edit the event (granted by owner)',

  sort_weight INT NOT NULL DEFAULT 0 COMMENT 'Display order',
  notes TEXT NULL COMMENT 'Admin notes',
  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),

  UNIQUE KEY uniq_calendar_event_attendees_public_id (public_id),
  UNIQUE KEY uniq_calendar_event_attendees_workspace_public_id (workspace_id, public_id),
  UNIQUE KEY uniq_calendar_event_attendees_event_user (event_id, user_id),
  KEY idx_calendar_event_attendees_workspace_user (workspace_id, user_id),

  CONSTRAINT fk_calendar_event_attendees_workspace FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  CONSTRAINT fk_calendar_event_attendees_event FOREIGN KEY (event_id) REFERENCES calendar_events(id) ON DELETE SET NULL,
  CONSTRAINT fk_calendar_event_attendees_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Calendar event attendees with RSVP and edit permission';
