// Package ai contains Huma operation handlers for the AI provider CRUD
// endpoints (/workspaces/{wsId}/ai/providers) and the per-user MCP token
// endpoints (/workspaces/{wsId}/me/mcp-tokens).
//
// SECURITY POLICY
//
// Plaintext LLM API keys flow through this package only as inbound request
// bodies. They are encrypted via internal/crypto and the plaintext is
// dropped before any handler returns. NO handler in this package may call
// Queries.FindProviderForDecrypt; that query is reserved for
// internal/ai/providers/. The depguard rule in apps/api/.golangci.yml
// keeps internal/crypto importable here for Encrypt only.
package ai

import (
	"database/sql"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/crypto"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// Deps is the dependency bundle for handlers in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Cipher  *crypto.Cipher
}

func httpErr(spec *apierrors.Spec) error {
	return huma.NewError(spec.Status, spec.Code+": "+spec.Message)
}

// Provider is the public DTO for an ai_providers row. It NEVER carries
// the ciphertext nor the plaintext API key.
type Provider struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	BaseURL      string    `json:"baseUrl,omitempty"`
	DefaultModel string    `json:"defaultModel,omitempty"`
	APIKeyMasked string    `json:"apiKeyMasked"`
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// CreateProviderInput is the body for POST /workspaces/{wsId}/ai/providers.
type CreateProviderInput struct {
	WsID string `path:"wsId"`
	Body struct {
		Kind         string `json:"kind" enum:"anthropic,openai,google,ollama,openai_compat"`
		Name         string `json:"name" minLength:"1" maxLength:"100"`
		BaseURL      string `json:"baseUrl,omitempty" maxLength:"255"`
		DefaultModel string `json:"defaultModel,omitempty" maxLength:"100"`
		// APIKey is the plaintext provider API key. It is write-only and
		// is dropped from memory as soon as it is sealed by the cipher.
		APIKey string `json:"apiKey" minLength:"8" maxLength:"512" doc:"Plaintext provider API key (write-only)"`
	}
}

// CreateProviderOutput returns the newly created provider, masked.
type CreateProviderOutput struct {
	Body Provider
}

// ListProvidersInput is the query for GET /workspaces/{wsId}/ai/providers.
type ListProvidersInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListProvidersOutput is the response for GET /workspaces/{wsId}/ai/providers.
type ListProvidersOutput struct {
	Body struct {
		Total     int64      `json:"total"`
		Providers []Provider `json:"providers"`
	}
}

// PatchProviderInput is the body for PATCH /workspaces/{wsId}/ai/providers/{providerId}.
type PatchProviderInput struct {
	WsID       string `path:"wsId"`
	ProviderID string `path:"providerId"`
	Body       struct {
		// APIKey is the new plaintext provider API key. The old key is
		// overwritten in place; the previous ciphertext is unrecoverable.
		APIKey string `json:"apiKey" minLength:"8" maxLength:"512" doc:"New plaintext provider API key (write-only)"`
	}
}

// PatchProviderOutput is an opaque ack — it never returns the new key.
type PatchProviderOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// DeleteProviderInput is the path for DELETE /workspaces/{wsId}/ai/providers/{providerId}.
type DeleteProviderInput struct {
	WsID       string `path:"wsId"`
	ProviderID string `path:"providerId"`
}

// DeleteProviderOutput is the ack for DELETE /workspaces/{wsId}/ai/providers/{providerId}.
type DeleteProviderOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

// McpTokenSummary is the masked DTO for an mcp_tokens row. It NEVER
// carries the plaintext token nor its hash.
type McpTokenSummary struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	TokenPrefix string    `json:"tokenPrefix"`
	Scopes      []string  `json:"scopes"`
	LastUsedAt  time.Time `json:"lastUsedAt,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// CreateMcpTokenInput is the body for POST /workspaces/{wsId}/me/mcp-tokens.
type CreateMcpTokenInput struct {
	WsID string `path:"wsId"`
	Body struct {
		Name   string   `json:"name" minLength:"1" maxLength:"255"`
		Scopes []string `json:"scopes"`
	}
}

// CreateMcpTokenOutput returns the newly minted MCP token. The plaintext
// `token` field is the ONLY place in the API where a token is ever
// returned, and only on this single response.
type CreateMcpTokenOutput struct {
	Body struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		Token       string    `json:"token" doc:"Plaintext bearer token, shown only once"`
		TokenPrefix string    `json:"tokenPrefix"`
		Scopes      []string  `json:"scopes"`
		CreatedAt   time.Time `json:"createdAt"`
	}
}

// ListMcpTokensInput is the query for GET /workspaces/{wsId}/me/mcp-tokens.
type ListMcpTokensInput struct {
	WsID   string `path:"wsId"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Offset int32  `query:"offset" minimum:"0" default:"0"`
}

// ListMcpTokensOutput is the response for GET /workspaces/{wsId}/me/mcp-tokens.
type ListMcpTokensOutput struct {
	Body struct {
		Total  int64             `json:"total"`
		Tokens []McpTokenSummary `json:"tokens"`
	}
}

// DeleteMcpTokenInput is the path for DELETE /workspaces/{wsId}/me/mcp-tokens/{tokenId}.
type DeleteMcpTokenInput struct {
	WsID    string `path:"wsId"`
	TokenID string `path:"tokenId"`
}

// DeleteMcpTokenOutput is the ack.
type DeleteMcpTokenOutput struct {
	Body struct {
		Ok bool `json:"ok"`
	}
}

func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

func nullTime(t sql.NullTime) time.Time {
	if t.Valid {
		return t.Time
	}
	return time.Time{}
}
