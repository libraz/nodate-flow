-- Structural checks: the shapes an implementation is allowed to rely on.
--
-- These are deliberately narrow. They assert the properties another
-- product's code would break on, not the full column list — a suite that
-- pinned every column would fail on additive changes the contract
-- explicitly permits.

-- Every core table is present.
CALL nf_conformance_assert(
  (SELECT COUNT(*) FROM information_schema.tables
   WHERE table_schema = DATABASE()
     AND table_name IN ('workspaces', 'users', 'calendars', 'calendar_events', 'events')) = 5,
  'the core tables workspaces, users, calendars, calendar_events and events must all exist');

-- The event log's primary key is what makes it tailable. A consumer polls
-- `WHERE id > last_seen`, so the column has to be monotonic and wide
-- enough that a long-lived deployment never wraps.
CALL nf_conformance_assert(
  (SELECT column_type = 'bigint unsigned' AND extra LIKE '%auto_increment%'
   FROM information_schema.columns
   WHERE table_schema = DATABASE() AND table_name = 'events' AND column_name = 'id'),
  'events.id must be BIGINT UNSIGNED AUTO_INCREMENT so consumers can tail the log by id');

-- Public identity is a 16-byte UUID everywhere, never the auto-increment.
-- An implementation that exposed the internal id would leak row counts and
-- break the moment two deployments merge.
CALL nf_conformance_assert(
  (SELECT COUNT(*) FROM information_schema.columns
   WHERE table_schema = DATABASE()
     AND table_name IN ('workspaces', 'users', 'calendars', 'calendar_events', 'events')
     AND column_name = 'public_id'
     AND column_type = 'binary(16)'
     AND is_nullable = 'NO') = 5,
  'public_id must be a NOT NULL BINARY(16) on every core table');

-- Cross-layer columns must stay optional, so a deployment that does not
-- host the layer giving them meaning can still insert.
CALL nf_conformance_assert(
  (SELECT is_nullable = 'YES'
   FROM information_schema.columns
   WHERE table_schema = DATABASE() AND table_name = 'calendar_events' AND column_name = 'task_id'),
  'calendar_events.task_id must be nullable so a deployment without a task layer can write events');

CALL nf_conformance_assert(
  (SELECT COUNT(*) FROM information_schema.columns
   WHERE table_schema = DATABASE() AND table_name = 'events'
     AND column_name IN ('task_id', 'actor_user_id')
     AND is_nullable = 'YES') = 2,
  'events.task_id and events.actor_user_id must be nullable');

-- The guard triggers are part of the contract, not an optional extra: an
-- implementation that loaded the tables but skipped them would accept
-- writes this document says are refused.
CALL nf_conformance_assert(
  (SELECT COUNT(*) FROM information_schema.triggers
   WHERE trigger_schema = DATABASE()
     AND trigger_name IN ('trg_calendar_events_projection_guard_ins',
                          'trg_calendar_events_projection_guard_upd',
                          'trg_calendar_events_projection_guard_del')) = 3,
  'all three calendar_events projection guard triggers must be installed');
