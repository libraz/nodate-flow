-- ====================================
-- trg_task_review_decisions_immutable
-- Review decisions are append-only. A new submission carries any later
-- decision so prior approval / rejection cannot be rewritten.
-- Rows are never deleted directly; FK cascades from the task and the
-- workspace remove them and do not fire triggers.
-- ====================================
DELIMITER $$

CREATE TRIGGER trg_task_review_decisions_immutable
BEFORE UPDATE ON task_review_decisions
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000'
    SET MESSAGE_TEXT = 'task review decisions are immutable';
END$$

CREATE TRIGGER trg_task_review_decisions_immutable_delete
BEFORE DELETE ON task_review_decisions
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000'
    SET MESSAGE_TEXT = 'task review decisions cannot be deleted';
END$$

DELIMITER ;
