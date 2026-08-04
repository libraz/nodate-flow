-- Cross-layer foreign key: events.triggered_by_signal_id -> signals.id
--
-- See fk_calendar_events_task.sql for why core columns carry flow-layer
-- references. In a core-only deployment this column is always NULL.
ALTER TABLE events
  ADD CONSTRAINT fk_events_triggered_by_signal
  FOREIGN KEY (triggered_by_signal_id) REFERENCES signals(id) ON DELETE SET NULL;
