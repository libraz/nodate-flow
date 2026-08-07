/**
 * MCP token scope vocabulary.
 *
 * The source of truth is Go: `mcp.SupportedScopes` in
 * `apps/flow-api/internal/mcp/scopes.go`, which both the issuance handler
 * (rejecting anything outside it) and the per-tool scope gate read. The
 * vocabulary does not reach the OpenAPI schema — the request field is a
 * bare `string[]` — so there is no generated artifact to import it from.
 *
 * It is therefore mirrored here and pinned to the Go file by
 * `__tests__/scope-vocabulary.test.ts`, which parses `scopes.go` and
 * fails when the two lists diverge. Adding a scope in Go without adding
 * it here breaks that test rather than quietly leaving a scope the UI
 * can never grant.
 */

export const MCP_TOKEN_SCOPES = ['read:workspace', 'write:workspace'] as const;

export type McpTokenScope = (typeof MCP_TOKEN_SCOPES)[number];

/**
 * Scopes preselected in the create dialog. Every MCP tool requires a
 * non-empty scope, so a token issued with none authenticates, streams,
 * and lists tools while refusing every call. Read access is the safe
 * default that still produces a usable token.
 */
export const DEFAULT_MCP_TOKEN_SCOPES: readonly McpTokenScope[] = ['read:workspace'];

export interface McpTokenScopeOption {
  scope: McpTokenScope;
  /** i18n key (settings namespace) for the checkbox label. */
  labelKey: string;
  /** i18n key (settings namespace) for the one-line explanation. */
  helpKey: string;
}

/** Checkbox rows, in the order the dialog renders them. */
export const MCP_TOKEN_SCOPE_OPTIONS: readonly McpTokenScopeOption[] = [
  {
    scope: 'read:workspace',
    labelKey: 'workspace.mcp_tokens.dialog.scope.read',
    helpKey: 'workspace.mcp_tokens.dialog.scope.read_help',
  },
  {
    scope: 'write:workspace',
    labelKey: 'workspace.mcp_tokens.dialog.scope.write',
    helpKey: 'workspace.mcp_tokens.dialog.scope.write_help',
  },
];
