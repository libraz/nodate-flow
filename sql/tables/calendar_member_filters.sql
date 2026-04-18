-- ====================================
-- calendar_member_filters
-- Per-subscriber toggle to hide specific members within a shared calendar.
-- Negative-list pattern: rows exist only for hidden members. If no row
-- exists for a (subscription, target_user) pair, the member is visible.
-- ====================================
CREATE TABLE calendar_member_filters (
  id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT 'Internal PK, never exposed',
  subscription_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to calendar_subscriptions.id (the viewer)',
  target_user_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to users.id (the member being hidden)',

  enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT 'Enabled flag',
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE KEY uniq_calendar_member_filters_sub_target (subscription_id, target_user_id),
  KEY idx_calendar_member_filters_target_user (target_user_id),

  CONSTRAINT fk_calendar_member_filters_subscription FOREIGN KEY (subscription_id) REFERENCES calendar_subscriptions(id) ON DELETE CASCADE,
  CONSTRAINT fk_calendar_member_filters_target FOREIGN KEY (target_user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='Per-subscriber member visibility filters for shared calendars';
