-- ====================================
-- trg_tasks_derived_state_guard
-- Enforces the convention that tasks.derived_state is computed from
-- constraints + events and may only be mutated by the events engine.
-- Any non-engine UPDATE that changes derived_state is rejected with
-- SQLSTATE '45000'.
--
-- The events engine (apps/flow-api/internal/events/...) opts in by
-- setting the session variable @nf_derived_state_engine = 1 prior to
-- its UPDATE; a non-NULL value bypasses the guard for that session.
-- Setting the variable back to NULL (or opening a new connection) re-
-- arms the guard.
-- ====================================
DELIMITER $$

CREATE TRIGGER trg_tasks_derived_state_guard
BEFORE UPDATE ON tasks
FOR EACH ROW
BEGIN
  IF NEW.derived_state <> OLD.derived_state
     AND @nf_derived_state_engine IS NULL THEN
    SIGNAL SQLSTATE '45000'
      SET MESSAGE_TEXT = 'derived_state mutation must go through the events engine';
  END IF;
END$$

DELIMITER ;
