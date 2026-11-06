-- name: CreateAiSuggestionBatch :execresult
-- Provenance is invalid until every canonical reference has been inserted.
INSERT INTO ai_suggestion_batches (
  public_id, workspace_id, user_id, input_count, provenance_valid
) VALUES (
  sqlc.arg('public_id'),
  sqlc.arg('workspace_id'),
  sqlc.arg('user_id'),
  sqlc.arg('input_count'),
  FALSE
);

-- name: CreateAiSuggestionBatchInput :exec
INSERT INTO ai_suggestion_batches_inputs (batch_id, task_public_id)
VALUES (sqlc.arg('batch_id'), sqlc.arg('task_public_id'));

-- name: MarkAiSuggestionBatchProvenanceValid :execrows
-- Complete the one-way provenance transition only when stored references
-- exactly match the expected count, including explicitly captured emptiness.
UPDATE ai_suggestion_batches b
SET provenance_valid = TRUE
WHERE b.id = sqlc.arg('id')
  AND b.provenance_valid = FALSE
  AND b.input_count = (
    SELECT COUNT(*)
    FROM ai_suggestion_batches_inputs i
    WHERE i.batch_id = b.id
  );

-- name: AuthorizeAiSuggestionBatch :one
-- The view enforces initiating actor, current workspace membership, and the
-- current ACL/existence of every task reference. Use this query from the
-- caller's transaction-bound *Queries handle for command authorization.
SELECT b.id
FROM v_ai_suggestion_readable_batches b
WHERE b.workspace_id = sqlc.arg('workspace_id')
  AND b.user_id = sqlc.arg('user_id')
  AND b.public_id = sqlc.arg('public_id')
LIMIT 1;
