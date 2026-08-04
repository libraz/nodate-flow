-- Cross-layer foreign key: events.task_id -> tasks.id
--
-- See fk_calendar_events_task.sql for why core columns carry flow-layer
-- references. In a core-only deployment this column is always NULL.
ALTER TABLE events
  ADD CONSTRAINT fk_events_task
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE;
