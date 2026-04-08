// Package inbox - triage.go is the endpoint that asks
// the AI orchestrator to score the workspace inbox and return
// per-item recommended actions. The route is registered separately
// from the v1 inbox routes (Register) because it needs the orchestrator
// dependency and lives behind the workspace-scoped router group.
package inbox

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// TriageDeps is the dependency bundle for the triage endpoint. It is a
// separate struct from Deps because the v1 inbox routes do not need an
// AI orchestrator and we want to keep handler wiring narrow.
type TriageDeps struct {
	Deps
	AI *ai.Orchestrator
}

// InboxTriageSuggestion is the per-item DTO returned to the client.
type InboxTriageSuggestion struct {
	InboxItemID       string  `json:"inboxItemId"`
	Score             float32 `json:"score"`
	RecommendedAction string  `json:"recommendedAction"`
	Reasoning         string  `json:"reasoning"`
}

// InboxTriageInputBody is the JSON body for POST /workspaces/{wsId}/inbox/triage.
type InboxTriageInputBody struct {
	Limit int `json:"limit,omitempty" minimum:"1" maximum:"50" doc:"Number of inbox items to score (default 20, max 50)"`
}

// InboxTriageInput is the request for POST /workspaces/{wsId}/inbox/triage.
type InboxTriageInput struct {
	WorkspaceID string `path:"wsId"`
	Body        InboxTriageInputBody
}

// InboxTriageOutputBody is the response body for POST /workspaces/{wsId}/inbox/triage.
type InboxTriageOutputBody struct {
	Suggestions []InboxTriageSuggestion `json:"suggestions"`
}

// InboxTriageOutput is the response envelope for the triage endpoint.
type InboxTriageOutput struct {
	Body InboxTriageOutputBody
}

// RegisterTriage wires POST /workspaces/{wsId}/inbox/triage. The caller
// must attach RequireAuth + RequireWorkspaceMember on the chi group it
// hands in.
func RegisterTriage(api huma.API, deps TriageDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "inbox-triage",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/inbox/triage",
		Summary:     "Ask the AI orchestrator to score and recommend actions for inbox items",
	}, Triage(deps))
}

// Triage handles POST /workspaces/{wsId}/inbox/triage.
func Triage(deps TriageDeps) func(context.Context, *InboxTriageInput) (*InboxTriageOutput, error) {
	return func(ctx context.Context, in *InboxTriageInput) (*InboxTriageOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsID, err := resolveWorkspace(ctx, deps.DB, in.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}
		if deps.AI == nil {
			return nil, httpErr(apierrors.AiProviderNotConfigured)
		}
		limit := in.Body.Limit
		if limit <= 0 {
			limit = 20
		}
		if limit > 50 {
			limit = 50
		}
		suggestions, err := deps.AI.ProposeInboxTriage(ctx, wsID, limit)
		if err != nil {
			switch {
			case errors.Is(err, ai.ErrNoProvider):
				return nil, httpErr(apierrors.AiProviderNotConfigured)
			case errors.Is(err, ai.ErrDailyBudgetExceeded):
				return nil, httpErr(apierrors.AiCostGuardExceeded)
			case errors.Is(err, ai.ErrParse):
				return nil, httpErr(apierrors.AiResponseParseFailed)
			default:
				return nil, httpErr(apierrors.AiProviderUpstreamCallFailed)
			}
		}
		out := &InboxTriageOutput{}
		out.Body.Suggestions = make([]InboxTriageSuggestion, 0, len(suggestions))
		for _, s := range suggestions {
			out.Body.Suggestions = append(out.Body.Suggestions, InboxTriageSuggestion{
				InboxItemID:       s.InboxItemID,
				Score:             s.Score,
				RecommendedAction: s.RecommendedAction,
				Reasoning:         s.Reasoning,
			})
		}
		return out, nil
	}
}
