-- Cross-layer foreign key: events.actor_agent_id -> ai_agents.id
--
-- See fk_calendar_events_task.sql for why core columns carry flow-layer
-- references. In a core-only deployment this column is always NULL.
ALTER TABLE events
  ADD CONSTRAINT fk_events_actor_agent
  FOREIGN KEY (actor_agent_id) REFERENCES ai_agents(id) ON DELETE SET NULL;
