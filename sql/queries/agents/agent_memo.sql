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
-- Read the raw agent_memo JSON for a task. The column is NOT NULL and
-- defaults to '{}', so a matching row always answers with an object; a task
-- that is absent or disabled answers with no row at all, which callers treat
-- as an empty memo. workspace_id is required for tenant scope.
-- task-visibility: not-applicable — every caller is a background pass with no
-- reader: the timer-driven auto-action scan and the queued agent run. Neither
-- has an actor whose visibility could scope this, and a predicate written
-- against a zero actor would silently answer every read with an empty memo,
-- which resets the handoff loop budget and the attempt counter rather than
-- withholding anything. workspace_id is the bound instead. The memo is
-- decoded into an attempt count, a handoff count and a handoff status, and
-- none of the memo text is copied into an event payload, a log line or a
-- response; the reader-facing projection of this column is
-- FindTaskByPublicId, which resolves the task through the shared task ACL
-- before it runs.
SELECT t.agent_memo
FROM tasks t
WHERE t.workspace_id = ?
  AND t.id = ?
  AND t.enabled = TRUE
LIMIT 1;
