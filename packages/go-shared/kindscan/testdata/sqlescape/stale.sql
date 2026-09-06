-- Input for kindscan's own tests, not a query this repository runs.
--
-- The marker on the first statement covers a name deliberately outside
-- the registry and is doing its job. The marker on the second covers a
-- declared kind, so it suppresses nothing and is itself the finding — an
-- escape left behind after the literal that earned it was fixed silently
-- covers whatever is written on that line next.

INSERT INTO events (public_id, type) VALUES (?, 'fixture.deliberate'); -- kindscan:undeclared

INSERT INTO events (public_id, type) VALUES (?, 'task.created'); -- kindscan:undeclared
