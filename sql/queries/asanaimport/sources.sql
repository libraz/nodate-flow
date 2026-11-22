-- name: GetWorkspaceMemberRoleForImport :one
-- Role and liveness of one user's membership in the workspace. A disabled
-- membership still answers so the importer can say why it refuses the user.
SELECT role, enabled
FROM workspace_members
WHERE workspace_id = ?
  AND user_id = ?
LIMIT 1;

-- name: ListUsersByEmailsForImport :many
-- Resolve source-system e-mail addresses to users. Disabled accounts are
-- returned with their flag so the importer can report them instead of
-- treating them as unknown.
SELECT id, public_id, email, display_name, enabled
FROM users
WHERE email IN (sqlc.slice('emails'))
ORDER BY id;

-- name: ListProjectsForImport :many
-- Enabled projects of the workspace, for matching mapped project names.
SELECT id, public_id, name, slug, identifier, enabled
FROM projects
WHERE workspace_id = ?
  AND enabled = TRUE
ORDER BY id;

-- name: ListProjectMembersForImport :many
-- Enabled project memberships for the given projects.
SELECT project_id, user_id, role
FROM project_members
WHERE workspace_id = ?
  AND project_id IN (sqlc.slice('project_ids'))
  AND enabled = TRUE
ORDER BY project_id, user_id, id;

-- name: ListLabelsForImport :many
-- Enabled labels of the workspace, matched by name.
SELECT id, public_id, project_id, name
FROM labels
WHERE workspace_id = ?
  AND enabled = TRUE
ORDER BY id;

-- name: CountEnabledSigninAllowlistForImport :one
-- Number of live OAuth sign-in allowlist entries; the allowlist is
-- instance-wide, so there is no workspace to scope by.
SELECT COUNT(*) AS total
FROM oauth_signin_allowlist
WHERE enabled = TRUE;

-- name: ListSourcedTasksInWorkspace :many
-- Live tasks whose source_uid starts with the prefix, across every project
-- of the workspace. source_uid_key is NULL for a soft-deleted row, so a
-- deleted task never matches.
-- task-visibility: not-applicable — operator import tooling, no viewer
SELECT
  id,
  public_id,
  project_id,
  source_uid,
  parent_task_id,
  title,
  description,
  due_on,
  started_on,
  derived_state,
  completion_policy
FROM tasks
WHERE workspace_id = ?
  AND source_uid_key LIKE CONCAT(sqlc.arg('prefix'), '%')
ORDER BY id;

-- name: CountTasksWithSourceUIDByProject :many
-- Live tasks carrying any source_uid, per project. Subtracting the sourced
-- count of a run gives the orphaned tasks.
SELECT project_id, COUNT(*) AS total
FROM tasks
WHERE workspace_id = ?
  AND source_uid_key IS NOT NULL
GROUP BY project_id
ORDER BY project_id;

-- name: ListSourcedCommentsByTasks :many
-- Live comments of the given tasks whose source_uid starts with the prefix.
SELECT id, public_id, task_id, source_uid, author_id, body
FROM comments
WHERE workspace_id = ?
  AND task_id IN (sqlc.slice('task_ids'))
  AND source_uid_key LIKE CONCAT(sqlc.arg('prefix'), '%')
ORDER BY task_id, id;

-- name: ListSourcedAttachmentsByTasks :many
-- Live attachments of the given tasks whose source_uid starts with the
-- prefix, with the owning storage object. uploaded_at NULL means the object
-- was reserved but never confirmed present in storage.
SELECT
  a.id,
  a.task_id,
  a.source_uid,
  a.storage_object_id,
  so.uploaded_at
FROM attachments a
JOIN storage_objects so ON so.id = a.storage_object_id
WHERE a.workspace_id = ?
  AND a.task_id IN (sqlc.slice('task_ids'))
  AND a.source_uid_key LIKE CONCAT(sqlc.arg('prefix'), '%')
ORDER BY a.task_id, a.id;

-- name: ListTaskActorsForTasks :many
-- Enabled user actors (assignee, reviewer, watcher, approver) of the tasks.
SELECT task_id, user_id, role
FROM task_actors
WHERE workspace_id = ?
  AND task_id IN (sqlc.slice('task_ids'))
  AND kind = 'user'
  AND enabled = TRUE
ORDER BY task_id, id;

-- name: ListTaskLabelsForTasks :many
-- Enabled label links of the tasks.
SELECT task_id, label_id
FROM task_labels
WHERE workspace_id = ?
  AND task_id IN (sqlc.slice('task_ids'))
  AND enabled = TRUE
ORDER BY task_id, id;

-- name: ListBlockingDependenciesForTasks :many
-- Enabled blocks edges whose source side is one of the tasks. id and
-- public_id identify the edge itself, which removal is keyed by.
SELECT id, public_id, from_task_id, to_task_id
FROM task_dependencies
WHERE workspace_id = ?
  AND from_task_id IN (sqlc.slice('task_ids'))
  AND kind = 'blocks'
  AND enabled = TRUE
ORDER BY from_task_id, id;
