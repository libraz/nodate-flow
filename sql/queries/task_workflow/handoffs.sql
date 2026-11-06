-- Human task-role handoff queries.

-- name: ListDueTaskHandoffsForUpdate :many
-- The event and resolution are committed by one RunOnce transaction. SKIP
-- LOCKED lets multiple bounded workers claim disjoint handoffs safely; only
-- handoff rows are locked so a held task or users row never hides a due one.
SELECT
  h.id,
  h.workspace_id,
  h.task_id,
  BIN_TO_UUID(h.public_id) AS handoff_public_id,
  BIN_TO_UUID(t.public_id) AS task_public_id,
  h.role,
  CAST(COALESCE(BIN_TO_UUID(from_user.public_id), '') AS CHAR(36)) AS from_user_public_id,
  CAST(COALESCE(BIN_TO_UUID(to_user.public_id), '') AS CHAR(36)) AS to_user_public_id
FROM task_handoffs h
INNER JOIN tasks t ON t.id = h.task_id AND t.workspace_id = h.workspace_id AND t.enabled = TRUE
LEFT JOIN users from_user ON from_user.id = h.from_user_id
LEFT JOIN users to_user ON to_user.id = h.to_user_id
WHERE h.status = 'pending'
  AND h.due_at IS NOT NULL
  AND h.due_at <= sqlc.arg(now)
ORDER BY h.due_at ASC, h.id ASC
LIMIT ?
FOR UPDATE OF h SKIP LOCKED;

-- name: ExpireTaskHandoffDue :execrows
UPDATE task_handoffs
SET status = 'expired', resolved_at = sqlc.arg(now)
WHERE workspace_id = sqlc.arg(workspace_id)
  AND task_id = sqlc.arg(task_id)
  AND id = sqlc.arg(id)
  AND status = 'pending'
  AND due_at IS NOT NULL
  AND due_at <= sqlc.arg(now);

-- name: ListTaskHumanActorsForRole :many
SELECT
  ta.id AS actor_id,
  BIN_TO_UUID(ta.public_id) AS actor_public_id,
  ta.user_id,
  BIN_TO_UUID(u.public_id) AS user_public_id
FROM task_actors ta
INNER JOIN users u ON u.id = ta.user_id AND u.enabled = TRUE
WHERE ta.workspace_id = ?
  AND ta.task_id = ?
  AND ta.role = ?
  AND ta.kind = 'user'
  AND ta.enabled = TRUE
ORDER BY ta.created_at ASC, ta.id ASC;

-- name: FindTaskHumanActorForRole :one
SELECT
  ta.id AS actor_id,
  BIN_TO_UUID(ta.public_id) AS actor_public_id,
  ta.user_id,
  BIN_TO_UUID(u.public_id) AS user_public_id
FROM task_actors ta
INNER JOIN users u ON u.id = ta.user_id AND u.enabled = TRUE
WHERE ta.workspace_id = ?
  AND ta.task_id = ?
  AND ta.user_id = ?
  AND ta.role = ?
  AND ta.kind = 'user'
  AND ta.enabled = TRUE
LIMIT 1;

-- name: CreateTaskHandoff :exec
INSERT INTO task_handoffs (
  public_id,
  workspace_id,
  task_id,
  role,
  from_user_id,
  to_user_id,
  requested_by_user_id,
  due_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindPendingTaskHandoffForRole :one
SELECT id
FROM task_handoffs
WHERE workspace_id = ?
  AND task_id = ?
  AND role = ?
  AND status = 'pending'
LIMIT 1;

-- name: FindTaskHandoffForUpdate :one
-- The caller already holds the task lock; only the handoff row is locked here.
SELECT
  h.id,
  BIN_TO_UUID(h.public_id) AS handoff_public_id,
  h.workspace_id,
  h.task_id,
  h.role,
  h.from_user_id,
  COALESCE(BIN_TO_UUID(from_user.public_id), '') AS from_user_public_id,
  h.to_user_id,
  COALESCE(BIN_TO_UUID(to_user.public_id), '') AS to_user_public_id,
  h.requested_by_user_id,
  h.status,
  h.due_at,
  h.created_at
FROM task_handoffs h
LEFT JOIN users from_user ON from_user.id = h.from_user_id
LEFT JOIN users to_user ON to_user.id = h.to_user_id
INNER JOIN tasks t ON t.id = h.task_id AND t.enabled = TRUE
WHERE h.workspace_id = ?
  AND h.task_id = ?
  AND h.public_id = ?
  AND t.workspace_id = h.workspace_id
LIMIT 1
FOR UPDATE OF h;

-- name: ResolveTaskHandoff :execrows
UPDATE task_handoffs
SET status = ?, resolved_at = CURRENT_TIMESTAMP(3)
WHERE workspace_id = ?
  AND task_id = ?
  AND public_id = ?
  AND status = 'pending';

-- name: RemoveTaskHumanActorForHandoff :execrows
UPDATE task_actors
SET enabled = FALSE
WHERE workspace_id = ?
  AND task_id = ?
  AND user_id = ?
  AND role = ?
  AND kind = 'user'
  AND enabled = TRUE;

-- name: IsEnabledTaskHumanActorForHandoff :one
SELECT EXISTS (
  SELECT 1
  FROM task_actors
  WHERE workspace_id = ?
    AND task_id = ?
    AND user_id = ?
    AND role = ?
    AND kind = 'user'
    AND enabled = TRUE
) AS is_actor;

-- name: FindTaskActorForRemoval :one
-- Read under the task lock so a removal can resolve the removed holder's
-- pending handoff in the same transaction.
SELECT kind, role, user_id
FROM task_actors
WHERE workspace_id = ?
  AND task_id = ?
  AND public_id = ?
  AND enabled = TRUE
LIMIT 1
FOR UPDATE;

-- name: FindPendingTaskHandoffFromHolderForUpdate :one
SELECT
  h.public_id,
  BIN_TO_UUID(h.public_id) AS handoff_public_id,
  COALESCE(BIN_TO_UUID(from_user.public_id), '') AS from_user_public_id,
  COALESCE(BIN_TO_UUID(to_user.public_id), '') AS to_user_public_id
FROM task_handoffs h
LEFT JOIN users from_user ON from_user.id = h.from_user_id
LEFT JOIN users to_user ON to_user.id = h.to_user_id
WHERE h.workspace_id = ?
  AND h.task_id = ?
  AND h.role = ?
  AND h.from_user_id = ?
  AND h.status = 'pending'
LIMIT 1
FOR UPDATE OF h;
