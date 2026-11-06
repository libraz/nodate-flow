-- Safe event-to-batch projection for proposal readers. It exposes event and
-- batch identity only; payload/reasoning remains in the protected event row.
CREATE OR REPLACE VIEW v_ai_suggestion_timeline_events AS
SELECT
  e.id AS event_id,
  b.id AS batch_id,
  b.public_id AS batch_public_id,
  e.workspace_id,
  b.user_id AS owner_user_id,
  e.actor_user_id,
  e.type AS event_type
FROM events e
INNER JOIN ai_suggestion_batches b
  ON b.workspace_id = e.workspace_id
 AND b.public_id = CASE
   WHEN IS_UUID(e.payload_json->>'$.suggestion_batch_id')
   THEN UUID_TO_BIN(e.payload_json->>'$.suggestion_batch_id', 0)
   ELSE NULL
 END
 AND b.user_id = e.actor_user_id
WHERE e.enabled = TRUE
  AND b.enabled = TRUE
  AND e.type IN ('ai.suggestion.proposed', 'ai.suggestion.edited');
