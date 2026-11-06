-- ====================================
-- trg_task_handoffs_immutable
-- Handoff identity and participants never change after request. The only
-- legal update resolves a still-pending row exactly once.
-- Rows are never deleted directly; FK cascades from the task and the
-- workspace remove them and do not fire triggers.
-- ====================================
DELIMITER $$

CREATE TRIGGER trg_task_handoffs_immutable
BEFORE UPDATE ON task_handoffs
FOR EACH ROW
BEGIN
  IF NEW.public_id <> OLD.public_id
     OR NEW.workspace_id <> OLD.workspace_id
     OR NEW.task_id <> OLD.task_id
     OR NEW.role <> OLD.role
     OR NOT (NEW.from_user_id <=> OLD.from_user_id)
     OR NOT (NEW.to_user_id <=> OLD.to_user_id)
     OR NOT (NEW.requested_by_user_id <=> OLD.requested_by_user_id)
     OR NOT (NEW.due_at <=> OLD.due_at)
     OR NEW.created_at <> OLD.created_at
     OR OLD.status <> 'pending'
     OR NEW.status NOT IN ('accepted','declined','expired','cancelled')
     OR NEW.resolved_at IS NULL THEN
    SIGNAL SQLSTATE '45000'
      SET MESSAGE_TEXT = 'task handoff may only be resolved once';
  END IF;
END$$

CREATE TRIGGER trg_task_handoffs_immutable_delete
BEFORE DELETE ON task_handoffs
FOR EACH ROW
BEGIN
  SIGNAL SQLSTATE '45000'
    SET MESSAGE_TEXT = 'task handoffs cannot be deleted';
END$$

DELIMITER ;
