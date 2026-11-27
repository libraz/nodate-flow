-- Retires the 'subtask_of' kind of task_dependencies; a subtask is modelled
-- only by tasks.parent_task_id.
--
-- Live subtask_of edges are first folded into parent_task_id: the child gets
-- the edge's target as parent when it has none and both tasks are enabled and
-- in the same project. A child that already has a different parent keeps it.
-- All subtask_of rows are then deleted and the ENUM is narrowed. Once the
-- column is in the new shape the file changes nothing, so it can be run again.

SET NAMES utf8mb4;

DROP PROCEDURE IF EXISTS flow_upgrade_0001;

DELIMITER //

CREATE PROCEDURE flow_upgrade_0001()
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'task_dependencies'
      AND COLUMN_NAME = 'kind'
      AND COLUMN_TYPE LIKE '%''subtask_of''%'
  ) THEN
    UPDATE tasks c
    INNER JOIN task_dependencies d
      ON d.from_task_id = c.id
     AND d.kind = 'subtask_of'
     AND d.enabled = TRUE
    INNER JOIN tasks p
      ON p.id = d.to_task_id
     AND p.enabled = TRUE
     AND p.project_id = c.project_id
    SET c.parent_task_id = p.id
    WHERE c.parent_task_id IS NULL
      AND c.enabled = TRUE;

    DELETE FROM task_dependencies WHERE kind = 'subtask_of';

    ALTER TABLE task_dependencies MODIFY COLUMN kind
      ENUM('blocks','relates','duplicates','retro_of')
      NOT NULL DEFAULT 'blocks'
      COMMENT 'Dependency kind. blocks: from_task waits on to_task. relates: informational link. duplicates: from_task duplicates to_task. retro_of: created by the signal_judge Applier when an event-day signal triggers a retrospective draft task; from_task is the new draft, to_task is the original task whose lifecycle finished and prompted the retro.';
  END IF;
END//

DELIMITER ;

CALL flow_upgrade_0001();

DROP PROCEDURE flow_upgrade_0001;
