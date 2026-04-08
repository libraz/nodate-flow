package ai

import (
	"context"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/crypto"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// IMPORTANT: Handlers in this file MUST NOT call
// deps.Queries.FindProviderForDecrypt. That query is reserved for the
// internal/ai/providers/ package family. Decryption is exclusively the
// Provider abstraction's responsibility; handlers only ever encrypt
// inbound keys and emit masked DTOs.

// CreateProvider handles POST /workspaces/{wsId}/ai/providers.
func CreateProvider(deps Deps) func(context.Context, *CreateProviderInput) (*CreateProviderOutput, error) {
	return func(ctx context.Context, in *CreateProviderInput) (*CreateProviderOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		if deps.Cipher == nil {
			return nil, httpErr(apierrors.AiProviderNotConfigured)
		}

		// Sealed key is built without ever stashing the plaintext anywhere
		// other than this stack frame. The slog call below records only the
		// masked prefix/suffix.
		var (
			sealed []byte
			prefix string
			suffix string
		)
		{
			plain := in.Body.APIKey
			prefix = crypto.APIKeyPrefix(plain)
			suffix = crypto.APIKeySuffix(plain)
			s, err := deps.Cipher.Encrypt([]byte(plain))
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			sealed = s
			// Best-effort: drop the plaintext from this scope before any
			// further work that could panic / log.
			plain = ""
			_ = plain
		}
		// Wipe the inbound body field so a downstream middleware that
		// happens to log the request struct cannot see the plaintext.
		in.Body.APIKey = ""

		pub := types.New()
		var baseURL, defaultModel = nullableString(in.Body.BaseURL), nullableString(in.Body.DefaultModel)
		if _, err := deps.Queries.CreateProvider(ctx, generated.CreateProviderParams{
			PublicID:         pub,
			WorkspaceID:      ws.ID,
			Kind:             generated.AiProvidersKind(in.Body.Kind),
			Name:             in.Body.Name,
			BaseUrl:          baseURL,
			ApiKeyCiphertext: sealed,
			ApiKeyPrefix:     prefix,
			ApiKeySuffix:     suffix,
			DefaultModel:     defaultModel,
		}); err != nil {
			slog.ErrorContext(ctx, "ai provider create failed",
				slog.String("workspaceId", ws.PublicID.String()),
				slog.String("apiKeyPrefix", prefix),
			)
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &CreateProviderOutput{Body: Provider{
			ID:           pub.String(),
			Kind:         in.Body.Kind,
			Name:         in.Body.Name,
			BaseURL:      in.Body.BaseURL,
			DefaultModel: in.Body.DefaultModel,
			APIKeyMasked: maskKey(prefix, suffix),
		}}
		return out, nil
	}
}

// ListProviders handles GET /workspaces/{wsId}/ai/providers.
func ListProviders(deps Deps) func(context.Context, *ListProvidersInput) (*ListProvidersOutput, error) {
	return func(ctx context.Context, in *ListProvidersInput) (*ListProvidersOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := deps.Queries.ListProvidersForWorkspace(ctx, generated.ListProvidersForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListProvidersOutput{}
		out.Body.Providers = make([]Provider, 0, len(rows))
		for _, r := range rows {
			out.Body.Providers = append(out.Body.Providers, rowToProvider(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// PatchProvider handles PATCH /workspaces/{wsId}/ai/providers/{providerId}.
// It rotates the stored API key. The previous ciphertext is unrecoverable
// after this call returns.
func PatchProvider(deps Deps) func(context.Context, *PatchProviderInput) (*PatchProviderOutput, error) {
	return func(ctx context.Context, in *PatchProviderInput) (*PatchProviderOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		if deps.Cipher == nil {
			return nil, httpErr(apierrors.AiProviderNotConfigured)
		}
		pub, err := types.Parse(in.ProviderID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		var (
			sealed []byte
			prefix string
			suffix string
		)
		{
			plain := in.Body.APIKey
			prefix = crypto.APIKeyPrefix(plain)
			suffix = crypto.APIKeySuffix(plain)
			s, encErr := deps.Cipher.Encrypt([]byte(plain))
			if encErr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			sealed = s
			plain = ""
			_ = plain
		}
		in.Body.APIKey = ""

		if err := deps.Queries.UpdateProviderKey(ctx, generated.UpdateProviderKeyParams{
			ApiKeyCiphertext: sealed,
			ApiKeyPrefix:     prefix,
			ApiKeySuffix:     suffix,
			WorkspaceID:      ws.ID,
			PublicID:         pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &PatchProviderOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// DeleteProvider handles DELETE /workspaces/{wsId}/ai/providers/{providerId}.
func DeleteProvider(deps Deps) func(context.Context, *DeleteProviderInput) (*DeleteProviderOutput, error) {
	return func(ctx context.Context, in *DeleteProviderInput) (*DeleteProviderOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.ProviderID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		if err := deps.Queries.DeleteProvider(ctx, generated.DeleteProviderParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &DeleteProviderOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
