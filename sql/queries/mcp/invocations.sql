-- name: LogMcpInvocation :execlastid
-- Append a redacted MCP tool invocation record. arguments_redacted_json and
-- result_redacted_json MUST already be filtered through the redaction layer.
INSERT INTO mcp_invocations (
  public_id,
  workspace_id,
  user_id,
  agent_id,
  task_id,
  tool_name,
  arguments_redacted_json,
  result_redacted_json,
  status,
  error_code,
  duration_ms,
  invoked_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
