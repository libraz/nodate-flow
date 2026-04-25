-- v_task_list
-- Active (non-archived) task projection for list / board views.
-- Thin filter over v_task_list_all; column definitions live in the base view.
CREATE OR REPLACE ALGORITHM=MERGE VIEW v_task_list AS
SELECT v.*
FROM v_task_list_all v
WHERE v.archived_at IS NULL;
