-- name: FindLensByPublicTokenHash :one
-- Look up a publicly shared lens by the SHA-256 hex of its URL token.
-- Used by unauthenticated public share endpoints; the caller hashes the
-- token from the path before calling, so the plaintext never reaches a
-- query log. Only returns enabled + public lenses.
SELECT
  l.id,
  l.public_id,
  l.workspace_id,
  w.public_id AS workspace_public_id,
  l.project_id,
  l.name,
  l.description,
  l.lens_json,
  l.shared_at,
  l.safety_checked_at,
  l.created_at
FROM lenses l
INNER JOIN workspaces w ON w.id = l.workspace_id AND w.enabled = TRUE
WHERE l.public_token_hash = ?
  AND l.is_public = TRUE
  AND l.enabled = TRUE
LIMIT 1;

-- name: ListPublicLensTasks :many
-- Resolve a publicly shared lens's task projection.
--
-- Returns a minimal, public-safe row set: title, status, priority and
-- due_on. The projection names no person: an unauthenticated reader is
-- never told who a task is assigned to. Internal ids and workspace
-- metadata are likewise excluded so this query can back the
-- unauthenticated GET /public/lenses/{token} endpoint.
--
-- The hard cap of 200 rows is enforced by the caller (see
-- apps/flow-api/internal/http/handlers/lenses/resolve.go) and by the LIMIT
-- bind parameter; public shares are not paginated.
--
-- Optional project_id narrows to a single project when the lens is
-- project-scoped; pass NULL for workspace-wide lenses. Filter knobs
-- mirror the closed Lens grammar:
--   * state_filter   (string)  - matches v_task_list.derived_state when non-empty
--   * priority_min   (nullable int) - tasks with priority >= value
--   * priority_max   (nullable int) - tasks with priority <= value
--   * due_from       (nullable date) - due_on >= value
--   * due_to         (nullable date) - due_on <= value
-- All filters are AND-combined; pass empty / NULL to skip a knob.
--
-- task-visibility: not-applicable — the share page is unauthenticated, so
-- there is no actor to compare a task against. The projection is narrowed
-- to visibility = 'public' instead, which is strictly inside what the
-- actor rule would allow for any reader.
SELECT
  v.public_id,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on
FROM v_task_list v
WHERE v.workspace_id = ?
  AND v.visibility = 'public'
  AND (sqlc.narg(project_id) IS NULL OR v.project_id = sqlc.narg(project_id))
  AND (sqlc.arg(state_filter) = '' OR v.derived_state = sqlc.arg(state_filter))
  AND (sqlc.narg(priority_min) IS NULL OR v.priority >= sqlc.narg(priority_min))
  AND (sqlc.narg(priority_max) IS NULL OR v.priority <= sqlc.narg(priority_max))
  AND (sqlc.narg(due_from) IS NULL OR (v.due_on IS NOT NULL AND v.due_on >= sqlc.narg(due_from)))
  AND (sqlc.narg(due_to) IS NULL OR (v.due_on IS NOT NULL AND v.due_on <= sqlc.narg(due_to)))
ORDER BY v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ?;

-- name: SetLensPublic :execrows
-- Enable public sharing on a lens. Stores the SHA-256 hex of the share
-- URL token minted by the caller; the plaintext is returned to the
-- publisher once and is not recoverable afterwards.
-- No-op if the lens is already public (WHERE is_public = FALSE guard).
UPDATE lenses
SET is_public = TRUE,
    public_token_hash = ?,
    shared_at = NOW(3)
WHERE workspace_id = ?
  AND public_id = ?
  AND is_public = FALSE
  AND enabled = TRUE;

-- name: SetLensPrivate :execrows
-- Revoke public sharing on a lens. Clears the token hash so the URL
-- stops resolving and re-publishing has to mint a fresh token.
-- No-op if the lens is already private (WHERE is_public = TRUE guard).
UPDATE lenses
SET is_public = FALSE,
    public_token_hash = NULL
WHERE workspace_id = ?
  AND public_id = ?
  AND is_public = TRUE
  AND enabled = TRUE;

-- name: UpdateLensSafetyCheck :exec
-- Record the timestamp of the latest AI safety check for a public lens.
UPDATE lenses
SET safety_checked_at = NOW(3)
WHERE id = ?;
