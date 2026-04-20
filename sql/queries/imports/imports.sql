-- name: CreateImportJob :execlastid
-- Insert a new import job into the queue.
INSERT INTO import_jobs (
  public_id, workspace_id, project_id, initiated_by_user_id, source, config_json
) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListImportJobsForWorkspace :many
-- List import jobs for a workspace with total count.
SELECT
  ij.public_id,
  ij.workspace_id,
  ij.source,
  ij.status,
  ij.total_items,
  ij.processed_items,
  ij.failed_items,
  ij.started_at,
  ij.completed_at,
  ij.created_at,
  COUNT(*) OVER() AS total
FROM import_jobs ij
WHERE ij.workspace_id = ?
  AND ij.enabled = TRUE
ORDER BY ij.created_at DESC, ij.id DESC
LIMIT ? OFFSET ?;

-- name: FindImportJobByPublicId :one
-- Find a single import job by public id.
SELECT
  ij.id,
  ij.public_id,
  ij.workspace_id,
  ij.project_id,
  ij.initiated_by_user_id,
  ij.source,
  ij.status,
  ij.total_items,
  ij.processed_items,
  ij.failed_items,
  ij.config_json,
  ij.error_log,
  ij.started_at,
  ij.completed_at,
  ij.notes,
  ij.created_at
FROM import_jobs ij
WHERE ij.workspace_id = ?
  AND ij.public_id = ?
  AND ij.enabled = TRUE;

-- name: UpdateImportJobStatus :exec
-- Update the status and timing fields of an import job.
UPDATE import_jobs
SET status       = ?,
    started_at   = ?,
    completed_at = ?,
    error_log    = ?
WHERE workspace_id = ?
  AND id = ?
  AND enabled = TRUE;

-- name: UpdateImportJobProgress :exec
-- Update the progress counters of an import job.
UPDATE import_jobs
SET processed_items = ?,
    failed_items    = ?
WHERE workspace_id = ?
  AND id = ?
  AND enabled = TRUE;

-- name: CancelImportJob :exec
-- Cancel a pending or running import job.
UPDATE import_jobs
SET status = 'cancelled'
WHERE workspace_id = ?
  AND id = ?
  AND status IN ('pending', 'running')
  AND enabled = TRUE;

-- name: FindRunningImportForProject :one
-- Check if a pending or running import already exists for a project.
SELECT
  ij.id,
  ij.public_id,
  ij.status,
  ij.source,
  ij.created_at
FROM import_jobs ij
WHERE ij.workspace_id = ?
  AND ij.project_id = ?
  AND ij.status IN ('pending', 'running')
  AND ij.enabled = TRUE
ORDER BY ij.created_at DESC, ij.id DESC
LIMIT 1;
