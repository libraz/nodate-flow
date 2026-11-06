-- Task review and completion policy queries. Every task-side lookup binds
-- both workspace_id and task_id so a public id or submission id cannot be
-- replayed under a sibling task's route.

-- name: GetTaskWorkflowSettings :one
SELECT completion_policy, work_revision, required_completion_constraint_ids, derived_state
FROM tasks
WHERE workspace_id = ? AND id = ? AND enabled = TRUE
LIMIT 1;

-- name: GetTaskWorkflowSettingsForUpdate :one
SELECT completion_policy, work_revision, required_completion_constraint_ids,
       derived_state
FROM tasks
WHERE workspace_id = ? AND id = ? AND enabled = TRUE
LIMIT 1
FOR UPDATE;

-- name: GetLatestTaskTransitionForSubmission :one
-- Every canonical state transition is appended in the same transaction
-- while holding the task row lock. The newest event distinguishes blocked
-- waiting work from legacy waiting work that remains submit-capable.
SELECT type
FROM events
WHERE workspace_id = ?
  AND task_id = ?
  AND type LIKE 'task.transition.%'
ORDER BY id DESC
LIMIT 1;

-- name: UpdateTaskCompletionPolicy :execrows
UPDATE tasks
SET completion_policy = ?,
    required_completion_constraint_ids = ?,
    updated_by_user_id = ?
WHERE workspace_id = ? AND id = ? AND enabled = TRUE;

-- name: FindEnabledTaskConstraintForWorkflow :one
SELECT id
FROM task_constraints
WHERE workspace_id = ? AND task_id = ? AND public_id = ? AND enabled = TRUE
LIMIT 1;

-- name: FindTaskWorkflowReviewer :one
-- A current read under the task lock, like IsCurrentTaskWorkflowReviewer.
SELECT u.id, BIN_TO_UUID(u.public_id) AS user_public_id
FROM task_actors ta
INNER JOIN users u ON u.id = ta.user_id AND u.enabled = TRUE
WHERE ta.workspace_id = ?
  AND ta.task_id = ?
  AND u.public_id = ?
  AND ta.kind = 'user'
  AND ta.role IN ('reviewer','approver')
  AND ta.enabled = TRUE
LIMIT 1
FOR SHARE OF ta;

-- name: IsCurrentTaskWorkflowReviewer :one
-- A current read: a concurrent actor removal is waited for, not read past.
SELECT EXISTS (
  SELECT 1
  FROM task_actors ta
  INNER JOIN users u ON u.id = ta.user_id AND u.enabled = TRUE
  WHERE ta.workspace_id = ?
    AND ta.task_id = ?
    AND ta.user_id = ?
    AND ta.kind = 'user'
    AND ta.role IN ('reviewer','approver')
    AND ta.enabled = TRUE
  FOR SHARE OF ta
) AS is_reviewer;

-- name: GetTaskWorkflowUserPublicID :one
SELECT BIN_TO_UUID(public_id) AS user_public_id
FROM users
WHERE id = ? AND enabled = TRUE
LIMIT 1;

-- name: FindTaskAttachmentDigestForSubmission :one
SELECT BIN_TO_UUID(a.public_id) AS attachment_public_id,
       LOWER(HEX(so.sha256)) AS sha256
FROM attachments a
INNER JOIN storage_objects so ON so.id = a.storage_object_id AND so.enabled = TRUE AND so.uploaded_at IS NOT NULL
WHERE a.workspace_id = ?
  AND a.task_id = ?
  AND a.public_id = ?
  AND a.enabled = TRUE
LIMIT 1;

-- name: CreateTaskSubmission :execlastid
INSERT INTO task_submissions (
  public_id,
  workspace_id,
  task_id,
  work_revision,
  submitted_by_user_id,
  reviewer_user_id,
  acceptance_criteria_json,
  evidence_urls_json,
  attachment_digests_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindTaskSubmissionForDecision :one
SELECT
  s.id AS submission_id,
  s.public_id AS submission_public_id,
  s.work_revision AS submission_revision,
  s.reviewer_user_id,
  COALESCE(BIN_TO_UUID(submitter.public_id), '') AS submitter_user_public_id,
  t.work_revision AS current_work_revision,
  s.created_at
FROM task_submissions s
INNER JOIN tasks t ON t.id = s.task_id AND t.enabled = TRUE
LEFT JOIN users submitter ON submitter.id = s.submitted_by_user_id
WHERE s.workspace_id = ?
  AND s.task_id = ?
  AND t.workspace_id = s.workspace_id
  AND s.public_id = ?
LIMIT 1;

-- name: ListRecentTaskSubmissionsForWorkflow :many
-- A task reader gets a bounded immutable history. CurrentSubmission is
-- derived by the handler from the row matching the task's current revision.
SELECT
  BIN_TO_UUID(s.public_id) AS submission_public_id,
  s.work_revision,
  COALESCE(BIN_TO_UUID(submitter.public_id), '') AS submitter_user_public_id,
  COALESCE(BIN_TO_UUID(reviewer.public_id), '') AS reviewer_user_public_id,
  s.acceptance_criteria_json,
  s.evidence_urls_json,
  s.attachment_digests_json,
  CAST(COALESCE(d.decision, '') AS CHAR(16)) AS decision,
  COALESCE(d.reason, '') AS reason,
  COALESCE(BIN_TO_UUID(decided_by.public_id), '') AS decided_by_user_public_id,
  d.created_at AS decided_at,
  s.created_at
FROM task_submissions s
INNER JOIN tasks t ON t.id = s.task_id AND t.enabled = TRUE
LEFT JOIN users submitter ON submitter.id = s.submitted_by_user_id
LEFT JOIN users reviewer ON reviewer.id = s.reviewer_user_id
LEFT JOIN task_review_decisions d ON d.submission_id = s.id
LEFT JOIN users decided_by ON decided_by.id = d.decided_by_user_id
WHERE s.workspace_id = ?
  AND s.task_id = ?
  AND t.workspace_id = s.workspace_id
ORDER BY s.id DESC
LIMIT 10;

-- name: FindTaskReviewDecisionForSubmission :one
SELECT id
FROM task_review_decisions
WHERE workspace_id = ? AND task_id = ? AND submission_id = ?
LIMIT 1;

-- name: CreateTaskReviewDecision :exec
INSERT INTO task_review_decisions (
  public_id,
  workspace_id,
  task_id,
  submission_id,
  decided_by_user_id,
  decision,
  reason
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: CanCompleteTask :one
-- Manual tasks retain the old path. Review-required tasks need the latest
-- submission for the current work revision approved, and every constraint
-- explicitly selected by policy still enabled and currently satisfied.
-- Every block is a current read so a caller's earlier snapshot cannot pass
-- the gate after the task lock is taken.
SELECT CASE
  WHEN t.completion_policy = 'manual' THEN TRUE
  WHEN EXISTS (
    SELECT 1
    FROM task_submissions s
    INNER JOIN task_review_decisions d ON d.submission_id = s.id
      AND d.workspace_id = s.workspace_id
      AND d.task_id = s.task_id
      AND d.decision = 'approved'
    WHERE s.workspace_id = t.workspace_id
      AND s.task_id = t.id
      AND s.work_revision = t.work_revision
      AND s.id = (
        SELECT MAX(s_latest.id)
        FROM task_submissions s_latest
        WHERE s_latest.workspace_id = t.workspace_id
          AND s_latest.task_id = t.id
          AND s_latest.work_revision = t.work_revision
        FOR SHARE
      )
    FOR SHARE
  )
  AND JSON_LENGTH(t.required_completion_constraint_ids) = (
    SELECT COUNT(*)
    FROM task_constraints c
    WHERE c.workspace_id = t.workspace_id
      AND c.task_id = t.id
      AND c.enabled = TRUE
      AND c.satisfied_at IS NOT NULL
      AND JSON_CONTAINS(
        t.required_completion_constraint_ids,
        JSON_QUOTE(BIN_TO_UUID(c.public_id)),
        '$'
      )
    FOR SHARE
  ) THEN TRUE
  ELSE FALSE
END AS can_complete
FROM tasks t
WHERE t.workspace_id = ? AND t.id = ? AND t.enabled = TRUE
LIMIT 1
FOR SHARE OF t;
