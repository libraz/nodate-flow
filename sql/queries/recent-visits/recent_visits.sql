-- name: UpsertRecentVisit :exec
-- Record or refresh a recent visit. If the user already visited this entity,
-- update the timestamp and title snapshot.
INSERT INTO user_recent_visits (
  public_id,
  workspace_id,
  user_id,
  entity_type,
  entity_public_id,
  entity_title
) VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  updated_at   = NOW(),
  entity_title = VALUES(entity_title);

-- name: ListRecentVisitsForUser :many
-- List the most recent visits for a user in a workspace, newest first.
SELECT
  urv.public_id,
  urv.entity_type,
  urv.entity_public_id,
  urv.entity_title,
  urv.updated_at,
  urv.created_at
FROM user_recent_visits urv
WHERE urv.workspace_id = ?
  AND urv.user_id = ?
  AND urv.enabled = TRUE
ORDER BY urv.updated_at DESC
LIMIT ?;
