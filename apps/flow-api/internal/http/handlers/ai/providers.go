package ai

import (
	"context"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
)

// IMPORTANT: Handlers in this file MUST NOT call
// deps.Queries.FindProviderForDecrypt. That query is reserved for the
// internal/ai/providers/ package family. Decryption is exclusively the
// Provider abstraction's responsibility; handlers only ever encrypt
// inbound keys and emit masked DTOs.

// modelDefaults captures the per-kind defaults used when auto-registering
// an ai_models row alongside a newly created provider. Operators can edit
// these values later once a standalone model admin surface lands.
type modelDefaults struct {
	contextWindow   uint32
	maxOutputTokens uint32
	supportsTools   bool
	supportsVision  bool
}

// defaultsForKind returns sensible defaults for the flagship model of
// each provider kind. Numbers are rounded public values and not meant to
// be exact; they only exist to avoid a zero context window that would
// confuse downstream components until the operator edits the row.
func defaultsForKind(kind string) modelDefaults {
	switch kind {
	case "anthropic":
		return modelDefaults{contextWindow: 200000, maxOutputTokens: 8192, supportsTools: true, supportsVision: true}
	case "openai":
		return modelDefaults{contextWindow: 128000, maxOutputTokens: 16384, supportsTools: true, supportsVision: true}
	case "google":
		return modelDefaults{contextWindow: 1000000, maxOutputTokens: 8192, supportsTools: true, supportsVision: true}
	default:
		// ollama / openai_compat / unknown: conservative defaults and no
		// assumed tool/vision support since the deployed model is unknown.
		return modelDefaults{contextWindow: 8192, maxOutputTokens: 2048, supportsTools: false, supportsVision: false}
	}
}

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
		providerInternalID, err := deps.Queries.CreateProvider(ctx, generated.CreateProviderParams{
			PublicID:         pub,
			WorkspaceID:      ws.ID,
			Kind:             generated.AiProvidersKind(in.Body.Kind),
			Name:             in.Body.Name,
			BaseUrl:          baseURL,
			ApiKeyCiphertext: sealed,
			ApiKeyPrefix:     prefix,
			ApiKeySuffix:     suffix,
			DefaultModel:     defaultModel,
		})
		if err != nil {
			slog.ErrorContext(ctx, "ai provider create failed",
				slog.String("workspaceId", ws.PublicID.String()),
				slog.String("apiKeyPrefix", prefix),
			)
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Short-term bridge for the AI-agents create flow: if the operator
		// supplied a default model, also register a matching ai_models row
		// with per-kind sensible defaults. Without this row, the agents
		// create dialog is stuck at the empty state because there is no
		// standalone model-registration UI yet. See
		// docs/bugs/2026-04-23-web-ai-agents-cannot-be-created-no-model-registration-path.md
		if in.Body.DefaultModel != "" && providerInternalID > 0 {
			d := defaultsForKind(in.Body.Kind)
			modelPub := types.New()
			const insertModel = `INSERT INTO ai_models (
				public_id, workspace_id, provider_id, name, display_name,
				context_window, max_output_tokens,
				input_price_micro_usd_per_mtok, output_price_micro_usd_per_mtok,
				supports_tools, supports_vision, enabled
			) VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, TRUE)`
			if _, err := deps.DB.ExecContext(ctx, insertModel,
				modelPub, ws.ID, uint32(providerInternalID),
				in.Body.DefaultModel, in.Body.DefaultModel,
				d.contextWindow, d.maxOutputTokens,
				d.supportsTools, d.supportsVision,
			); err != nil {
				// Log but do not fail the provider create; operators can
				// retry registering a model once a standalone model API
				// lands. Duplicate-key errors on re-create are expected if
				// the same (provider_id, model name) already exists.
				slog.ErrorContext(ctx, "ai model auto-register failed",
					slog.String("workspaceId", ws.PublicID.String()),
					slog.String("providerId", pub.String()),
					slog.String("modelName", in.Body.DefaultModel),
					slog.Any("error", err),
				)
			}
		}

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "ai_provider.create",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "ai_provider",
				ResourceID:   pub.String(),
				Metadata:     map[string]any{"kind": in.Body.Kind, "name": in.Body.Name},
			})
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
		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "ai_provider.update",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "ai_provider",
				ResourceID:   pub.String(),
			})
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
		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "ai_provider.delete",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "ai_provider",
				ResourceID:   pub.String(),
			})
		}
		out := &DeleteProviderOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
