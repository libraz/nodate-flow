-- Cross-layer foreign key: calendar_events.task_id -> tasks.id
--
-- task_id is a core column so that every writer, including a core-only
-- deployment with no tasks table, produces rows of the same shape. The
-- constraint cannot be declared in core because its target lives in the
-- flow layer, so it is added here once both layers are present.
-- ON DELETE action reproduced verbatim from the original declaration.
ALTER TABLE calendar_events
  ADD CONSTRAINT fk_calendar_events_task
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL;
