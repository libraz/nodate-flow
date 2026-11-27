package ai

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/packages/go-shared/crypto"
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
	contextWindow              uint32
	maxOutputTokens            uint32
	inputPriceMicroUSDPerMTok  int64
	outputPriceMicroUSDPerMTok int64
	supportsTools              bool
	supportsVision             bool
}

// defaultsForKind returns generic metadata for an explicit model whose
// capabilities are not in the current official-default catalog. Keep these
// values stable for custom and previously supported model IDs.
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

// defaultsForModel returns current public metadata and prices only when the
// exact official default is known. Other explicit model IDs keep the prior
// conservative per-kind metadata and unpriced status.
func defaultsForModel(kind, model string) modelDefaults {
	if model == "" || model != providers.OfficialDefaultModel(providers.Kind(kind)) {
		return defaultsForKind(kind)
	}
	switch kind {
	case "anthropic":
		return modelDefaults{
			contextWindow: 1_000_000, maxOutputTokens: 128_000,
			inputPriceMicroUSDPerMTok: 2_000_000, outputPriceMicroUSDPerMTok: 10_000_000,
			supportsTools: true, supportsVision: true,
		}
	case "openai":
		return modelDefaults{
			contextWindow: 1_050_000, maxOutputTokens: 128_000,
			inputPriceMicroUSDPerMTok: 100_000, outputPriceMicroUSDPerMTok: 500_000,
			supportsTools: true, supportsVision: true,
		}
	case "google":
		return modelDefaults{
			contextWindow: 1_048_576, maxOutputTokens: 65_536,
			inputPriceMicroUSDPerMTok: 1_500_000, outputPriceMicroUSDPerMTok: 7_500_000,
			supportsTools: true, supportsVision: true,
		}
	default:
		return defaultsForKind(kind)
	}
}

func resolveDefaultModel(kind, requested string) string {
	if requested != "" {
		return requested
	}
	return providers.OfficialDefaultModel(providers.Kind(kind))
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

		// Refuse a configuration that cannot become a provider, before it
		// reaches the table.
		//
		// The check already existed, inside providers.New, but nothing ran
		// it until the first completion — long after the row was written.
		// By then the error surfaced as "could not reach the provider" on
		// an unrelated request, and the row could not be repaired: PATCH
		// takes only the API key. Worse, the default provider is the most
		// recently created enabled row, so one bad row silently shadowed a
		// working one and the whole workspace's AI stopped, with nothing
		// pointing at the row that had just been added.
		//
		// EncryptedKey is a stand-in: the real sealed blob does not exist
		// yet at this point and Validate only checks that a key is present
		// for the kinds that require one.
		if in.Body.Kind != string(providers.KindOllama) && in.Body.APIKey == "" {
			return nil, httpErr(apierrors.ValidationBodyFieldMissing)
		}
		// Empty means the kind's official default (or none), so only a
		// supplied value is checked.
		if in.Body.DefaultModel != "" {
			name, ok := validModelName(in.Body.DefaultModel)
			if !ok {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			in.Body.DefaultModel = name
		}
		defaultModel := resolveDefaultModel(in.Body.Kind, in.Body.DefaultModel)
		if err := providers.Validate(providers.Config{
			Kind:         providers.Kind(in.Body.Kind),
			Name:         in.Body.Name,
			BaseURL:      in.Body.BaseURL,
			DefaultModel: defaultModel,
			EncryptedKey: []byte(in.Body.APIKey),
		}); err != nil {
			slog.InfoContext(ctx, "ai provider create rejected",
				slog.String("workspaceId", ws.PublicID.String()),
				slog.String("kind", in.Body.Kind),
				slog.Bool("hasBaseUrl", in.Body.BaseURL != ""),
				slog.String("err", err.Error()))
			return nil, createProviderError(err)
		}
		// Resolve the endpoint as well, so a hostname that points inside the
		// deployment's own network is refused here rather than at the first
		// completion. The connect-time guard in internal/ai/providers is what
		// actually enforces the policy; this is the part that can say which
		// field was wrong while the admin still has the form open.
		if err := providers.ValidateBaseURLDestination(ctx, in.Body.BaseURL); err != nil {
			slog.InfoContext(ctx, "ai provider create rejected",
				slog.String("workspaceId", ws.PublicID.String()),
				slog.String("kind", in.Body.Kind),
				slog.String("err", err.Error()))
			return nil, createProviderError(err)
		}

		// Sealed key is built without ever stashing the plaintext anywhere
		// other than this stack frame. The slog call below records only the
		// masked prefix/suffix.
		var (
			// NOT NULL column: an ollama provider without a key stores an
			// empty blob, not nil.
			sealed = []byte{}
			prefix string
			suffix string
		)
		if in.Body.APIKey != "" {
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
		var baseURL = nullableString(in.Body.BaseURL)
		defaultModelValue := nullableString(defaultModel)
		// The provider and its default model row land together, so a
		// provider never exists without the model its default names.
		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "ai.CreateProvider", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			q := deps.Queries.WithTx(tx.RawTx())
			if defaultModel != "" {
				full, err := enabledModelLimitReached(ctx, q, ws.ID)
				if err != nil {
					return err
				}
				if full {
					answered = httpErr(apierrors.AiModelLimitReached)
					return errAnswered
				}
			}
			providerInternalID, err := q.CreateProvider(ctx, generated.CreateProviderParams{
				PublicID:         pub,
				WorkspaceID:      ws.ID,
				Kind:             generated.AiProvidersKind(in.Body.Kind),
				Name:             in.Body.Name,
				BaseUrl:          baseURL,
				ApiKeyCiphertext: sealed,
				ApiKeyPrefix:     prefix,
				ApiKeySuffix:     suffix,
				DefaultModel:     defaultModelValue,
			})
			if err != nil {
				return err
			}
			if defaultModel == "" {
				return nil
			}
			_, err = q.InsertAIModel(ctx, insertModelParams(types.New(), ws.ID,
				uint32(providerInternalID), //#nosec G115 -- LastInsertId for ai_providers.id (INT UNSIGNED)
				in.Body.Kind, defaultModel, defaultModel))
			return err
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			slog.ErrorContext(ctx, "ai provider create failed",
				slog.String("workspaceId", ws.PublicID.String()),
				slog.String("apiKeyPrefix", prefix),
			)
			return nil, httpErr(apierrors.InternalUnexpected)
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
			DefaultModel: defaultModel,
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

		rows, err := deps.Queries.UpdateProviderKey(ctx, generated.UpdateProviderKeyParams{
			ApiKeyCiphertext: sealed,
			ApiKeyPrefix:     prefix,
			ApiKeySuffix:     suffix,
			WorkspaceID:      ws.ID,
			PublicID:         pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if rows == 0 {
			return nil, httpErr(apierrors.AiProviderNotFound)
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
		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "ai.DeleteProvider", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			q := deps.Queries.WithTx(tx.RawTx())
			providerID, err := q.LockEnabledProviderForUpdate(ctx, generated.LockEnabledProviderForUpdateParams{
				WorkspaceID: ws.ID,
				PublicID:    pub,
			})
			if errors.Is(err, sql.ErrNoRows) {
				answered = httpErr(apierrors.AiProviderNotFound)
				return errAnswered
			}
			if err != nil {
				return err
			}
			agents, err := q.CountEnabledAgentsForProvider(ctx, providerID)
			if err != nil {
				return err
			}
			if agents > 0 {
				answered = httpErr(apierrors.AiProviderInUse)
				return errAnswered
			}
			// The row is locked and enabled, so exactly one row changes.
			rows, err := q.DeleteProvider(ctx, generated.DeleteProviderParams{
				WorkspaceID: ws.ID,
				PublicID:    pub,
			})
			if err != nil {
				return err
			}
			if rows != 1 {
				return fmt.Errorf("ai: provider delete affected %d rows", rows)
			}
			return nil
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
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
