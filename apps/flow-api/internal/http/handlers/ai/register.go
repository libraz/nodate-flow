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
	}, CreateProvider(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-providers-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/ai/providers",
		Summary:     "List LLM providers (masked, no ciphertext)",
	}, ListProviders(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-providers-patch",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/ai/providers/{providerId}",
		Summary:     "Rotate an LLM provider API key",
	}, PatchProvider(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-providers-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/ai/providers/{providerId}",
		Summary:     "Soft-delete an LLM provider",
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
	}, CreateMcpToken(deps))

	huma.Register(api, huma.Operation{
		OperationID: "mcp-tokens-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/me/mcp-tokens",
		Summary:     "List the caller's MCP tokens (no plaintext)",
	}, ListMcpTokens(deps))

	huma.Register(api, huma.Operation{
		OperationID: "mcp-tokens-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/me/mcp-tokens/{tokenId}",
		Summary:     "Revoke an MCP bearer token",
	}, DeleteMcpToken(deps))
}
