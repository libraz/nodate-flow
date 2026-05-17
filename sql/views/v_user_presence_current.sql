-- v_user_presence_current
-- Materialised projection of the latest non-expired chat-platform presence
-- signal per `(workspace_id, user_id)`. Selects the most recent row from
-- `signals` whose `subject_type = 'user'` and whose `kind` is one of the
-- chat-platform presence kinds.
--
-- Designed as a view rather than a dedicated `user_presence` table so that
-- provider webhook handlers only ever write to one place (`signals`); the
-- view is consistent by construction the moment a presence signal lands.
-- See ADR 0008 (docs/adr/0008-signals-and-judge-loop.md) D5 for rationale.
--
-- The latest row per group is selected with the standard "self-join on MAX"
-- pattern (no window functions in views, MySQL 8.4 supports them but the
-- self-join form keeps the optimiser plan inspectable). Rows with a non-NULL
-- `expires_at` in the past are filtered out so that stale presence does not
-- bleed into the UI; the retention sweep will eventually delete them.
CREATE OR REPLACE VIEW v_user_presence_current AS
SELECT
  s.workspace_id,
  s.subject_id AS user_id,
  s.source,
  s.kind,
  s.payload_json,
  s.received_at,
  s.expires_at
FROM signals s
INNER JOIN workspaces w
  ON w.id = s.workspace_id AND w.enabled = TRUE
INNER JOIN (
  SELECT
    workspace_id,
    subject_id,
    MAX(received_at) AS latest_received_at
  FROM signals
  WHERE enabled = TRUE
    AND subject_type = 'user'
    AND subject_id IS NOT NULL
    AND kind IN ('discord.presence', 'slack.presence', 'teams.presence')
    AND (expires_at IS NULL OR expires_at >= NOW(3))
  GROUP BY workspace_id, subject_id
) latest
  ON latest.workspace_id = s.workspace_id
 AND latest.subject_id   = s.subject_id
 AND latest.latest_received_at = s.received_at
WHERE s.enabled = TRUE
  AND s.subject_type = 'user'
  AND s.kind IN ('discord.presence', 'slack.presence', 'teams.presence')
  AND (s.expires_at IS NULL OR s.expires_at >= NOW(3));
