-- ====================================
-- trg_task_submissions_immutable
-- Submissions are append-only evidence snapshots. Corrections are a new
-- submission rather than an in-place rewrite of reviewed content.
-- Rows are never deleted directly; FK cascades from the task and the
-- workspace remove them and do not fire triggers.
-- ====================================
DELIMITER $$

CREATE TRIGGER trg_task_submissions_immutable
BEFORE UPDATE ON task_submissions
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000'
    SET MESSAGE_TEXT = 'task submissions are immutable';
END$$

CREATE TRIGGER trg_task_submissions_immutable_delete
BEFORE DELETE ON task_submissions
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000'
    SET MESSAGE_TEXT = 'task submissions cannot be deleted';
END$$

DELIMITER ;
