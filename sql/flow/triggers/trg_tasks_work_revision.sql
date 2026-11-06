-- ====================================
-- trg_tasks_work_revision
-- A review approval is valid only for the exact editable work that was
-- submitted. Keep the revision increment next to task writes so REST,
-- MCP, workers, and attachment paths cannot forget it.
-- ====================================
DELIMITER $$

CREATE TRIGGER trg_tasks_work_revision
BEFORE UPDATE ON tasks
FOR EACH ROW
BEGIN
  IF NOT (NEW.title <=> OLD.title)
     OR NOT (NEW.description <=> OLD.description)
     OR NEW.work_revision <> OLD.work_revision THEN
    SET NEW.work_revision = OLD.work_revision + 1;
  END IF;
END$$

DELIMITER ;
