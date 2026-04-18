// Package providers - resolver.go is the production wiring that maps a
// workspace to its default LLM Provider. It is the ONLY production site
// that calls generated.Querier.FindProviderForDecrypt; depguard keeps that
// query fenced inside apps/flow-api/internal/ai/providers/.
package providers

import (
	"context"
	"errors"
	"fmt"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

// WorkspaceResolver is the production implementation of
// ai.ProviderResolver. It looks up the first enabled provider for a
// workspace via ListProvidersForWorkspace, then loads the sealed key
// blob via FindProviderForDecrypt, and finally constructs a Provider
// that will decrypt the blob lazily inside Complete.
type WorkspaceResolver struct {
	Queries   generated.Querier
	Decryptor Decryptor
}

// ErrNoEnabledProvider is returned by Default when a workspace has no
// enabled AI provider row. Callers map it to AI.PROVIDER.NOT_CONFIGURED
// at the HTTP / MCP boundary.
var ErrNoEnabledProvider = errors.New("ai/providers: no enabled provider for workspace")

// MockResolver is a ProviderResolver that always returns the same
// MockProvider regardless of workspace. It is wired in when the api
// boots with NF_AI_MOCK=1 and short-circuits both the
// ListProvidersForWorkspace lookup and any decryption.
type MockResolver struct {
	Provider Provider
}

// NewMockResolver constructs a MockResolver pre-loaded with the supplied
// provider. Pass NewMockProvider("") to use the default fixture path.
func NewMockResolver(p Provider) *MockResolver {
	return &MockResolver{Provider: p}
}

// Default implements ai.ProviderResolver. The workspaceID is ignored:
// the mock returns the same fixture provider for every caller.
func (r *MockResolver) Default(_ context.Context, _ uint32) (Provider, error) {
	if r == nil || r.Provider == nil {
		return nil, ErrNoEnabledProvider
	}
	return r.Provider, nil
}

// NewWorkspaceResolver constructs a WorkspaceResolver. Both arguments
// are required; a nil Decryptor would make Complete fail downstream.
func NewWorkspaceResolver(q generated.Querier, dec Decryptor) *WorkspaceResolver {
	return &WorkspaceResolver{Queries: q, Decryptor: dec}
}

// Default implements ai.ProviderResolver. It picks the first enabled
// provider row for the workspace (ListProvidersForWorkspace orders by
// created_at DESC), then resolves the sealed blob via
// FindProviderForDecrypt and constructs a Provider.
func (r *WorkspaceResolver) Default(ctx context.Context, workspaceID uint32) (Provider, error) {
	if r == nil || r.Queries == nil {
		return nil, ErrNoEnabledProvider
	}
	list, err := r.Queries.ListProvidersForWorkspace(ctx, generated.ListProvidersForWorkspaceParams{
		WorkspaceID: workspaceID,
		Limit:       1,
		Offset:      0,
	})
	if err != nil {
		return nil, fmt.Errorf("ai/providers: list workspace providers: %w", err)
	}
	if len(list) == 0 {
		return nil, ErrNoEnabledProvider
	}
	head := list[0]
	row, err := r.Queries.FindProviderForDecrypt(ctx, generated.FindProviderForDecryptParams{
		WorkspaceID: workspaceID,
		PublicID:    head.PublicID,
	})
	if err != nil {
		return nil, fmt.Errorf("ai/providers: load sealed key: %w", err)
	}
	cfg := Config{
		Kind:         Kind(row.Kind),
		Name:         row.Name,
		BaseURL:      row.BaseUrl.String,
		DefaultModel: row.DefaultModel.String,
		EncryptedKey: row.ApiKeyCiphertext,
		APIKeyPrefix: row.ApiKeyPrefix,
	}
	prov, err := New(cfg, r.Decryptor)
	// Drop the reference to the ciphertext held on the stack row. The
	// underlying byte slice is still owned by cfg / the provider, which
	// will decrypt-use-zero inside Complete.
	row.ApiKeyCiphertext = nil
	if err != nil {
		return nil, err
	}
	return prov, nil
}
