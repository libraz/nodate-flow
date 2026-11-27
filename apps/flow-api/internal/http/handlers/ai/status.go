package ai

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// GetAIStatusInput identifies the workspace in the route.
type GetAIStatusInput struct {
	WsID string `path:"wsId"`
}

// AIStatusOutput is the response for GET /workspaces/{wsId}/ai/status.
type AIStatusOutput struct {
	Body AIStatusBody
}

// AIStatusBody summarises which AI capabilities work for the workspace.
// It never carries credentials.
type AIStatusBody struct {
	Provider    AIStatusProvider    `json:"provider"`
	Embedding   AIStatusEmbedding   `json:"embedding"`
	AutoActions AIStatusAutoActions `json:"autoActions"`
}

// AIStatusProvider describes the provider AI features resolve to.
type AIStatusProvider struct {
	State string `json:"state" enum:"ready,no_default_model,none,unavailable"`
	Name  string `json:"name,omitempty"`
	Model string `json:"model,omitempty"`
}

// AIStatusEmbedding is the effective embedding status.
type AIStatusEmbedding struct {
	Status string `json:"status" enum:"enabled,disabled,unconfigured"`
}

// AIStatusAutoActions is the auto-action executor state.
type AIStatusAutoActions struct {
	Enabled         bool  `json:"enabled"`
	IntervalMinutes int32 `json:"intervalMinutes"`
}

// GetAIStatus reports the workspace's effective AI status. Readable by every
// workspace member, so it decrypts nothing and returns no key material.
func GetAIStatus(deps Deps) func(context.Context, *GetAIStatusInput) (*AIStatusOutput, error) {
	return func(ctx context.Context, _ *GetAIStatusInput) (*AIStatusOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		provider, err := aiStatusProvider(ctx, deps, ws.ID)
		if err != nil {
			return nil, err
		}
		embedding, err := embeddingStatus(ctx, deps, ws.ID)
		if err != nil {
			return nil, err
		}

		auto := AIStatusAutoActions{Enabled: defaultAutoActionEnabled, IntervalMinutes: defaultAutoActionIntervalMinutes}
		row, err := deps.Queries.GetAiSettings(ctx, ws.ID)
		if err != nil && err != sql.ErrNoRows {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err == nil {
			a := autoActionBodyFromRow(row)
			auto = AIStatusAutoActions{Enabled: a.Enabled, IntervalMinutes: int32(a.IntervalMinutes)} //#nosec G115 -- bounded 0..1440 by the settings endpoint
		}

		return &AIStatusOutput{Body: AIStatusBody{
			Provider:    provider,
			Embedding:   AIStatusEmbedding{Status: embedding},
			AutoActions: auto,
		}}, nil
	}
}

func aiStatusProvider(ctx context.Context, deps Deps, workspaceID uint32) (AIStatusProvider, error) {
	if deps.Resolver == nil {
		return AIStatusProvider{State: "unavailable"}, nil
	}
	if _, ok := deps.Resolver.(*providers.MockResolver); ok {
		return AIStatusProvider{State: "ready", Name: "mock"}, nil
	}
	rows, err := deps.Queries.ListProvidersForWorkspace(ctx, generated.ListProvidersForWorkspaceParams{
		WorkspaceID: workspaceID,
		Limit:       1,
		Offset:      0,
	})
	if err != nil {
		return AIStatusProvider{}, httpErr(apierrors.InternalUnexpected)
	}
	if len(rows) == 0 {
		return AIStatusProvider{State: "none"}, nil
	}
	row := rows[0]
	if row.DefaultModel.Valid && row.DefaultModel.String != "" {
		return AIStatusProvider{State: "ready", Name: row.Name, Model: row.DefaultModel.String}, nil
	}
	kind := providers.Kind(row.Kind)
	if kind == providers.KindOpenAICompat || kind == providers.KindOllama {
		return AIStatusProvider{State: "no_default_model", Name: row.Name}, nil
	}
	return AIStatusProvider{State: "ready", Name: row.Name, Model: providers.OfficialDefaultModel(kind)}, nil
}
