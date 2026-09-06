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

-- name: ListPendingImportJobs :many
-- The worker's claim candidates, oldest first. Claiming is a separate
-- conditional UPDATE, so this read takes no locks: a row listed here may
-- already have been taken by another replica by the time it is claimed.
SELECT
  ij.id,
  ij.public_id,
  ij.workspace_id,
  ij.project_id,
  ij.initiated_by_user_id,
  ij.source,
  ij.config_json
FROM import_jobs ij
WHERE ij.status = 'pending'
  AND ij.enabled = TRUE
ORDER BY ij.created_at ASC, ij.id ASC
LIMIT ?;

-- name: ClaimImportJob :execrows
-- Take ownership of one pending job. The status column is the claim: the
-- update matches only while the row is still pending, so exactly one
-- replica can move it to running. Callers MUST treat a zero row count as
-- "someone else got it" and move on.
UPDATE import_jobs
SET status     = 'running',
    started_at = NOW(3)
WHERE id = ?
  AND status = 'pending'
  AND enabled = TRUE;

-- name: FinishImportJob :exec
-- Move a claimed job to a terminal state and stamp the final counters.
-- Restricted to 'running' so a job cancelled mid-flight keeps the
-- cancelled status the API gave it.
UPDATE import_jobs
SET status          = ?,
    total_items     = ?,
    processed_items = ?,
    failed_items    = ?,
    error_log       = ?,
    completed_at    = NOW(3)
WHERE id = ?
  AND status = 'running'
  AND enabled = TRUE;

-- name: RecordImportJobProgress :exec
-- Publish the running counters so the polling UI can show movement.
UPDATE import_jobs
SET total_items     = ?,
    processed_items = ?,
    failed_items    = ?
WHERE id = ?
  AND enabled = TRUE;

-- name: FindImportJobStatusByID :one
-- Read the current status of a claimed job. The worker calls this while
-- it works so a cancellation issued through the API stops the run.
SELECT ij.status
FROM import_jobs ij
WHERE ij.id = ?
  AND ij.enabled = TRUE;

-- name: FailStuckImportJobs :execrows
-- Reap jobs left running by a worker that died mid-import. Without this
-- the row keeps its running status forever, which is the same "never
-- finishes" the queue exists to avoid. The cutoff is supplied by the
-- caller so the timeout stays configurable.
UPDATE import_jobs
SET status       = 'failed',
    error_log    = ?,
    completed_at = NOW(3)
WHERE status = 'running'
  AND started_at IS NOT NULL
  AND started_at < ?
  AND enabled = TRUE;

-- name: CountImportedTasksForJob :one
-- Progress counters as the worker last published them, for callers that
-- need the finished totals without re-reading the whole row.
SELECT ij.total_items, ij.processed_items, ij.failed_items
FROM import_jobs ij
WHERE ij.id = ?
  AND ij.enabled = TRUE;
