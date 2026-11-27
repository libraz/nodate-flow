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
		Description: "Encrypts and stores a new API key for the provider. The old key is overwritten in place; nothing else about the provider changes.",
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

	huma.Register(api, huma.Operation{
		OperationID: "ai-provider-models-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/ai/providers/{providerId}/models",
		Summary:     "List the models registered under an LLM provider",
		Description: "Lists every model registered under the provider, enabled and disabled, newest first, with its default flag and the number of enabled agents using it. Workspace admin only.",
		Tags:        []string{"AI"},
	}, ListProviderModels(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-provider-models-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/ai/providers/{providerId}/models",
		Summary:     "Register a model under an LLM provider",
		Description: "Registers a model identifier under the provider. A disabled model with the same name is re-enabled instead. The first model added to a provider without a default becomes its default. Workspace admin only.",
		Tags:        []string{"AI"},
	}, CreateProviderModel(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-provider-models-patch",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/ai/providers/{providerId}/models/{modelId}",
		Summary:     "Enable, disable or rename a provider model",
		Description: "Changes a model's enabled flag or display name. Disabling the provider's default model or a model used by enabled agents is refused, and a refused request applies nothing. Workspace admin only.",
		Tags:        []string{"AI"},
	}, PatchProviderModel(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-provider-default-model-set",
		Method:      http.MethodPut,
		Path:        "/workspaces/{wsId}/ai/providers/{providerId}/default-model",
		Summary:     "Set an LLM provider's default model",
		Description: "Points the provider's default model at one of its enabled models. AI features that run without an agent use the default model of the workspace's default provider. Workspace admin only.",
		Tags:        []string{"AI"},
	}, SetProviderDefaultModel(deps))

	huma.Register(api, huma.Operation{
		OperationID: "ai-provider-available-models-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/ai/providers/{providerId}/available-models",
		Summary:     "List the model identifiers the provider key can see",
		Description: "Asks the provider for the models its API key can use, as candidates for registration. Returns an empty list when the provider kind cannot enumerate models. Workspace admin only.",
		Tags:        []string{"AI"},
	}, ListAvailableModels(deps))
}

// RegisterMcpTokens wires the per-user MCP token endpoints. The caller
// MUST attach RequireWorkspaceMember to the underlying chi router.
func RegisterMcpTokens(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "mcp-tokens-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/me/mcp-tokens",
		Summary:     "Mint a new MCP bearer token (plaintext returned once)",
		Description: "Generates a new MCP bearer token (mcp_…) the caller can pass to a local MCP client to authenticate against /mcp. Requires an active user session. The plaintext token is returned exactly once; only the hash is stored.",
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
		Description: "Marks the token as revoked so future /mcp requests carrying it are rejected. Requires an active user session. Returns 404 MCP.TOKEN.NOT_FOUND when the token is already revoked.",
		Tags:        []string{"AI"},
	}, DeleteMcpToken(deps))
}
