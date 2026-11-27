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

// WorkspaceAIStatusOutput is the response for GET /workspaces/{wsId}/ai/status.
type WorkspaceAIStatusOutput struct {
	Body WorkspaceAIStatusBody
}

// WorkspaceAIStatusBody summarises which AI capabilities work for the workspace.
// It never carries credentials.
type WorkspaceAIStatusBody struct {
	Provider    WorkspaceAIStatusProvider    `json:"provider"`
	Embedding   WorkspaceAIStatusEmbedding   `json:"embedding"`
	AutoActions WorkspaceAIStatusAutoActions `json:"autoActions"`
}

// WorkspaceAIStatusProvider describes the provider AI features resolve to.
type WorkspaceAIStatusProvider struct {
	State string `json:"state" enum:"ready,no_default_model,none,unavailable"`
	Name  string `json:"name,omitempty"`
	Model string `json:"model,omitempty"`
}

// WorkspaceAIStatusEmbedding is the effective embedding status.
type WorkspaceAIStatusEmbedding struct {
	Status string `json:"status" enum:"enabled,disabled,unconfigured"`
}

// WorkspaceAIStatusAutoActions is the auto-action executor state.
type WorkspaceAIStatusAutoActions struct {
	Enabled         bool  `json:"enabled"`
	IntervalMinutes int32 `json:"intervalMinutes"`
}

// GetAIStatus reports the workspace's effective AI status. Readable by every
// workspace member, so it decrypts nothing and returns no key material.
func GetAIStatus(deps Deps) func(context.Context, *GetAIStatusInput) (*WorkspaceAIStatusOutput, error) {
	return func(ctx context.Context, _ *GetAIStatusInput) (*WorkspaceAIStatusOutput, error) {
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

		auto := WorkspaceAIStatusAutoActions{Enabled: defaultAutoActionEnabled, IntervalMinutes: defaultAutoActionIntervalMinutes}
		row, err := deps.Queries.GetAiSettings(ctx, ws.ID)
		if err != nil && err != sql.ErrNoRows {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err == nil {
			a := autoActionBodyFromRow(row)
			auto = WorkspaceAIStatusAutoActions{Enabled: a.Enabled, IntervalMinutes: int32(a.IntervalMinutes)} //#nosec G115 -- bounded 0..1440 by the settings endpoint
		}

		return &WorkspaceAIStatusOutput{Body: WorkspaceAIStatusBody{
			Provider:    provider,
			Embedding:   WorkspaceAIStatusEmbedding{Status: embedding},
			AutoActions: auto,
		}}, nil
	}
}

func aiStatusProvider(ctx context.Context, deps Deps, workspaceID uint32) (WorkspaceAIStatusProvider, error) {
	if deps.Resolver == nil {
		return WorkspaceAIStatusProvider{State: "unavailable"}, nil
	}
	if _, ok := deps.Resolver.(*providers.MockResolver); ok {
		return WorkspaceAIStatusProvider{State: "ready", Name: "mock"}, nil
	}
	rows, err := deps.Queries.ListProvidersForWorkspace(ctx, generated.ListProvidersForWorkspaceParams{
		WorkspaceID: workspaceID,
		Limit:       1,
		Offset:      0,
	})
	if err != nil {
		return WorkspaceAIStatusProvider{}, httpErr(apierrors.InternalUnexpected)
	}
	if len(rows) == 0 {
		return WorkspaceAIStatusProvider{State: "none"}, nil
	}
	row := rows[0]
	if row.DefaultModel.Valid && row.DefaultModel.String != "" {
		return WorkspaceAIStatusProvider{State: "ready", Name: row.Name, Model: row.DefaultModel.String}, nil
	}
	kind := providers.Kind(row.Kind)
	if kind == providers.KindOpenAICompat || kind == providers.KindOllama {
		return WorkspaceAIStatusProvider{State: "no_default_model", Name: row.Name}, nil
	}
	return WorkspaceAIStatusProvider{State: "ready", Name: row.Name, Model: providers.OfficialDefaultModel(kind)}, nil
}
