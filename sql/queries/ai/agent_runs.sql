-- name: EnqueueAgentRun :execlastid
-- Atomically enqueue a new run. The UNIQUE constraint on dedupe_key
-- causes a second scheduler replica to get a duplicate-entry error,
-- which callers translate to ErrDuplicate. No row is inserted on conflict.
INSERT INTO agent_runs (
  public_id,
  workspace_id,
  agent_id,
  dedupe_key,
  scheduled_at
) VALUES (?, ?, ?, ?, ?);

-- name: ClaimNextAgentRun :one
-- Pick the oldest pending run and mark it claimed in the same tx.
-- Callers wrap this in BEGIN / COMMIT; SELECT ... FOR UPDATE SKIP LOCKED
-- lets multiple workers race for rows without contention.
SELECT
  r.id,
  r.public_id,
  r.workspace_id,
  r.agent_id,
  r.dedupe_key,
  r.scheduled_at,
  r.attempts
FROM agent_runs r
WHERE r.status = 'pending'
  AND r.enabled = TRUE
ORDER BY r.scheduled_at ASC, r.id ASC
LIMIT 1
FOR UPDATE SKIP LOCKED;

-- name: MarkAgentRunClaimed :exec
-- Flip the row to claimed after ClaimNextAgentRun returns it.
UPDATE agent_runs
SET status = 'claimed',
    claimed_at = ?,
    attempts = attempts + 1
WHERE id = ?;

-- name: AckAgentRun :exec
-- Worker finished successfully.
UPDATE agent_runs
SET status = 'succeeded',
    finished_at = ?
WHERE id = ?;

-- name: NackAgentRun :exec
-- Worker failed; park the row with the error message. Retry policy
-- lives in the application layer so different agents can have
-- different budgets.
UPDATE agent_runs
SET status = 'failed',
    finished_at = ?,
    error_message = ?
WHERE id = ?;

-- name: PurgeFinishedAgentRuns :exec
-- Housekeeping: drop succeeded / failed rows older than the cutoff so
-- the table does not grow unbounded. Run from a cron or a startup task.
DELETE FROM agent_runs
WHERE status IN ('succeeded', 'failed')
  AND finished_at < ?;

-- name: GetLastSuccessfulAgentRun :one
-- Return the most recent succeeded run time for a given agent.
-- Used by the agent pre-flight check to determine if new events have
-- occurred since the last run.
SELECT scheduled_at
FROM agent_runs
WHERE workspace_id = ? AND agent_id = ? AND status = 'succeeded'
ORDER BY scheduled_at DESC
LIMIT 1;
