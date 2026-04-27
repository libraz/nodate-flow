-- v_workspace_activity
-- Unified read model that streams audit_logs, ai_invocations, and
-- mcp_invocations into one cursor-paginated activity timeline per
-- workspace. Built so the workspace dashboard / admin UI can issue a
-- single indexed query instead of three round-trips with manual merge.
--
-- Projection contract:
--   workspace_id            -- internal FK; required for filtering, never returned by API
--   public_id               -- BINARY(16); the source row's UUID (unique per source_table)
--   source                  -- 'audit' | 'ai' | 'mcp' (literal)
--   source_table            -- literal source table name (for debug / drill-down joins)
--   occurred_at             -- DATETIME(3); audit.occurred_at / ai.invoked_at / mcp.invoked_at
--   actor_user_public_id    -- BINARY(16) NULL; resolved via users.id when the source row has an actor
--   actor_kind              -- 'user' | 'agent' | 'system'
--   action                  -- audit.action / ai.purpose / mcp.tool_name
--   resource_type           -- audit.resource_type / 'ai_invocation' / 'mcp_invocation'
--   resource_public_id      -- audit.resource_public_id / source's own public_id for ai/mcp
--   severity                -- 'info' | 'warn' | 'error' (derived from status enum)
--
-- UNION ALL (not UNION) — rows are intrinsically distinct by (source,
-- public_id) and we want to skip the dedup sort. Each leg filters
-- enabled = TRUE on its source row to honour the soft-delete contract.
CREATE OR REPLACE VIEW v_workspace_activity AS
SELECT
  al.workspace_id,
  al.public_id,
  CAST('audit' AS CHAR(8) CHARACTER SET utf8mb4) AS source,
  CAST('audit_logs' AS CHAR(32) CHARACTER SET utf8mb4) AS source_table,
  al.occurred_at,
  actor.public_id AS actor_user_public_id,
  CAST(IF(al.actor_user_id IS NULL, 'system', 'user') AS CHAR(8) CHARACTER SET utf8mb4) AS actor_kind,
  al.action,
  al.resource_type,
  al.resource_public_id,
  CAST('info' AS CHAR(8) CHARACTER SET utf8mb4) AS severity
FROM audit_logs al
INNER JOIN workspaces w_a
  ON w_a.id = al.workspace_id AND w_a.enabled = TRUE
LEFT JOIN users actor
  ON actor.id = al.actor_user_id AND actor.enabled = TRUE
WHERE al.enabled = TRUE

UNION ALL

SELECT
  ai.workspace_id,
  ai.public_id,
  CAST('ai' AS CHAR(8) CHARACTER SET utf8mb4) AS source,
  CAST('ai_invocations' AS CHAR(32) CHARACTER SET utf8mb4) AS source_table,
  ai.invoked_at AS occurred_at,
  actor.public_id AS actor_user_public_id,
  CAST(
    CASE
      WHEN ai.agent_id IS NOT NULL THEN 'agent'
      WHEN ai.user_id IS NOT NULL THEN 'user'
      ELSE 'system'
    END
    AS CHAR(8) CHARACTER SET utf8mb4
  ) AS actor_kind,
  ai.purpose AS action,
  CAST('ai_invocation' AS CHAR(64) CHARACTER SET utf8mb4) AS resource_type,
  ai.public_id AS resource_public_id,
  CAST(
    CASE ai.status
      WHEN 'ok' THEN 'info'
      WHEN 'blocked' THEN 'warn'
      ELSE 'error'
    END
    AS CHAR(8) CHARACTER SET utf8mb4
  ) AS severity
FROM ai_invocations ai
INNER JOIN workspaces w_i
  ON w_i.id = ai.workspace_id AND w_i.enabled = TRUE
LEFT JOIN users actor
  ON actor.id = ai.user_id AND actor.enabled = TRUE
WHERE ai.enabled = TRUE

UNION ALL

SELECT
  mi.workspace_id,
  mi.public_id,
  CAST('mcp' AS CHAR(8) CHARACTER SET utf8mb4) AS source,
  CAST('mcp_invocations' AS CHAR(32) CHARACTER SET utf8mb4) AS source_table,
  mi.invoked_at AS occurred_at,
  actor.public_id AS actor_user_public_id,
  CAST(IF(mi.user_id IS NULL, 'system', 'user') AS CHAR(8) CHARACTER SET utf8mb4) AS actor_kind,
  mi.tool_name AS action,
  CAST('mcp_invocation' AS CHAR(64) CHARACTER SET utf8mb4) AS resource_type,
  mi.public_id AS resource_public_id,
  CAST(
    CASE mi.status
      WHEN 'ok' THEN 'info'
      WHEN 'denied' THEN 'warn'
      ELSE 'error'
    END
    AS CHAR(8) CHARACTER SET utf8mb4
  ) AS severity
FROM mcp_invocations mi
INNER JOIN workspaces w_m
  ON w_m.id = mi.workspace_id AND w_m.enabled = TRUE
LEFT JOIN users actor
  ON actor.id = mi.user_id AND actor.enabled = TRUE
WHERE mi.enabled = TRUE;
