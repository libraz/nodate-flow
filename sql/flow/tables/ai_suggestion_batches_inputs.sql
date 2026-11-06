-- ====================================
-- ai_suggestion_batches_inputs
-- Public task references quoted into one suggestion batch.
-- ====================================
CREATE TABLE ai_suggestion_batches_inputs (
  batch_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to ai_suggestion_batches.id',
  task_public_id BINARY(16) NOT NULL COMMENT 'Public UUID of a task quoted into the provider input',

  PRIMARY KEY (batch_id, task_public_id),
  KEY idx_ai_suggestion_batches_inputs_task_public_id (task_public_id, batch_id),

  -- No task FK: deletion must preserve provenance so stale output is denied.
  CONSTRAINT fk_ai_suggestion_batches_inputs_batch FOREIGN KEY (batch_id) REFERENCES ai_suggestion_batches(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Task resources quoted into an AI suggestion batch';
