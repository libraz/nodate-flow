-- name: UpdateTaskAgentMemo :exec
-- Merge the supplied JSON patch into the task's agent_memo column.
-- JSON_MERGE_PATCH applies RFC 7396 semantics: keys present in the patch
-- overwrite (or recursively merge object values), and explicit nulls in the
-- patch remove keys. COALESCE with JSON_OBJECT() handles the initial NULL
-- case so the first write does not need a separate INSERT path. workspace_id
-- is required as a defence-in-depth tenant scope.
UPDATE tasks
SET agent_memo = JSON_MERGE_PATCH(COALESCE(agent_memo, JSON_OBJECT()), CAST(? AS JSON))
WHERE workspace_id = ?
  AND id = ?
  AND enabled = TRUE;

-- name: GetTaskAgentMemo :one
-- Read the raw agent_memo JSON for a task. Returns NULL when unset; callers
-- treat that as an empty memo. workspace_id is required for tenant scope.
SELECT agent_memo
FROM tasks
WHERE workspace_id = ?
  AND id = ?
  AND enabled = TRUE
LIMIT 1;
