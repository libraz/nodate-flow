package ai

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterProviders wires the AI provider CRUD endpoints. The caller MUST
// attach RequireWorkspaceMember + RequireWorkspaceRole(Admin) to the
// underlying chi router.
func RegisterProviders(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "ai-providers-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/ai/providers",
		Summary:     "Register an LLM provider with an encrypted API key",
		Description: "Creates a workspace LLM provider row (kind, base URL, model defaults). The plaintext API key is encrypted at rest with the deployment cipher and never echoed back. Workspace admin only.",
		Tags:        []string{"AI"},
	}, CreateProvider(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-providers-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/ai/providers",
		Summary:     "List LLM providers (masked, no ciphertext)",
		Description: "Lists every LLM provider configured for the workspace with masked key suffixes only. Backs the AI Settings panel.",
		Tags:        []string{"AI"},
	}, ListProviders(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-providers-patch",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/ai/providers/{providerId}",
		Summary:     "Rotate an LLM provider API key",
		Description: "Re-encrypts and stores a new API key for the provider, optionally updating model defaults at the same time. The old key is overwritten in place.",
		Tags:        []string{"AI"},
	}, PatchProvider(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-providers-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/ai/providers/{providerId}",
		Summary:     "Soft-delete an LLM provider",
		Description: "Marks the provider as removed so subsequent AI calls fail closed with AI.PROVIDER.NOT_CONFIGURED. Historical ai_invocations rows remain queryable.",
		Tags:        []string{"AI"},
	}, DeleteProvider(deps))
}

// RegisterMcpTokens wires the per-user MCP token endpoints. The caller
// MUST attach RequireWorkspaceMember to the underlying chi router.
func RegisterMcpTokens(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "mcp-tokens-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/me/mcp-tokens",
		Summary:     "Mint a new MCP bearer token (plaintext returned once)",
		Description: "Generates a new MCP bearer token (mcp_…) the caller can pass to a local MCP client to authenticate against /mcp. The plaintext token is returned exactly once; only the hash is stored.",
		Tags:        []string{"AI"},
	}, CreateMcpToken(deps))

	huma.Register(api, huma.Operation{
		OperationID: "mcp-tokens-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/me/mcp-tokens",
		Summary:     "List the caller's MCP tokens (no plaintext)",
		Description: "Lists the caller's existing MCP tokens with label, last-used time, and creation time. Plaintext is never returned; rotate by issuing a new token and revoking the old one.",
		Tags:        []string{"AI"},
	}, ListMcpTokens(deps))

	huma.Register(api, huma.Operation{
		OperationID: "mcp-tokens-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/me/mcp-tokens/{tokenId}",
		Summary:     "Revoke an MCP bearer token",
		Description: "Marks the token as revoked so future /mcp requests carrying it are rejected. Idempotent.",
		Tags:        []string{"AI"},
	}, DeleteMcpToken(deps))
}
