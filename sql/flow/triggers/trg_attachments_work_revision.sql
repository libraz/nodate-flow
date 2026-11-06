-- ====================================
-- trg_attachments_work_revision
-- Attachment bytes are represented by the content-addressed storage
-- object's digest. Attaching, replacing, moving, or removing a file
-- changes the task work revision and invalidates prior review approval.
-- Only uploaded files count: a presign reserves the attachment row before
-- any bytes exist, so the revision moves when the upload is confirmed, and
-- reclaiming a never-uploaded reservation leaves it alone. uploaded_at only
-- ever goes from NULL to a value, so the current row answers for OLD too.
-- ====================================
DELIMITER $$

CREATE TRIGGER trg_attachments_work_revision_insert
AFTER INSERT ON attachments
FOR EACH ROW
BEGIN
  IF NEW.task_id IS NOT NULL AND NEW.enabled = TRUE
     AND EXISTS (SELECT 1 FROM storage_objects so
                 WHERE so.id = NEW.storage_object_id AND so.uploaded_at IS NOT NULL) THEN
    UPDATE tasks
    SET work_revision = work_revision + 1
    WHERE id = NEW.task_id AND workspace_id = NEW.workspace_id;
  END IF;
END$$

CREATE TRIGGER trg_attachments_work_revision_update
AFTER UPDATE ON attachments
FOR EACH ROW
BEGIN
  DECLARE old_counts BOOLEAN DEFAULT FALSE;
  DECLARE new_counts BOOLEAN DEFAULT FALSE;
  DECLARE moved BOOLEAN DEFAULT NOT (NEW.task_id <=> OLD.task_id)
    OR NEW.storage_object_id <> OLD.storage_object_id;

  SET old_counts = OLD.task_id IS NOT NULL AND OLD.enabled = TRUE
    AND EXISTS (SELECT 1 FROM storage_objects so
                WHERE so.id = OLD.storage_object_id AND so.uploaded_at IS NOT NULL);
  SET new_counts = NEW.task_id IS NOT NULL AND NEW.enabled = TRUE
    AND EXISTS (SELECT 1 FROM storage_objects so
                WHERE so.id = NEW.storage_object_id AND so.uploaded_at IS NOT NULL);

  IF old_counts AND (NOT new_counts OR moved) THEN
    UPDATE tasks
    SET work_revision = work_revision + 1
    WHERE id = OLD.task_id AND workspace_id = OLD.workspace_id;
  END IF;

  IF new_counts AND (NOT old_counts OR moved) THEN
    UPDATE tasks
    SET work_revision = work_revision + 1
    WHERE id = NEW.task_id AND workspace_id = NEW.workspace_id;
  END IF;
END$$

CREATE TRIGGER trg_attachments_work_revision_delete
AFTER DELETE ON attachments
FOR EACH ROW
BEGIN
  IF OLD.task_id IS NOT NULL AND OLD.enabled = TRUE
     AND EXISTS (SELECT 1 FROM storage_objects so
                 WHERE so.id = OLD.storage_object_id AND so.uploaded_at IS NOT NULL) THEN
    UPDATE tasks
    SET work_revision = work_revision + 1
    WHERE id = OLD.task_id AND workspace_id = OLD.workspace_id;
  END IF;
END$$

-- The upload being confirmed is when every attachment already pointing at
-- the reservation starts to count.
CREATE TRIGGER trg_storage_objects_attachment_work_revision
AFTER UPDATE ON storage_objects
FOR EACH ROW
BEGIN
  IF OLD.uploaded_at IS NULL AND NEW.uploaded_at IS NOT NULL THEN
    UPDATE tasks
    SET work_revision = work_revision + 1
    WHERE id IN (SELECT a.task_id FROM attachments a
                 WHERE a.storage_object_id = NEW.id
                   AND a.workspace_id = tasks.workspace_id
                   AND a.enabled = TRUE);
  END IF;
END$$

DELIMITER ;
