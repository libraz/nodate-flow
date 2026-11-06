-- ====================================
-- ai_invocations_inputs
-- Current task ACL inputs for each logged model invocation.
-- ====================================
CREATE TABLE ai_invocations_inputs (
  invocation_id INT UNSIGNED NOT NULL COMMENT 'Internal FK to ai_invocations.id',
  task_public_id BINARY(16) NOT NULL COMMENT 'Public UUID of a task quoted into the provider input',

  PRIMARY KEY (invocation_id, task_public_id),
  KEY idx_ai_invocations_inputs_task_public_id (task_public_id, invocation_id),

  -- Deliberately no FK to tasks: deleting a referenced task must leave
  -- provenance behind so ordinary readers fail closed instead of silently
  -- forgetting that the prompt once contained its content.
  CONSTRAINT fk_ai_invocations_inputs_invocation
    FOREIGN KEY (invocation_id) REFERENCES ai_invocations(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Task resources quoted into AI invocation inputs';
