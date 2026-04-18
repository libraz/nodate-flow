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

	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/inboxtriage"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
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

// deterministicFallback runs the pure inboxtriage rule engine over
// the workspace's current inbox and returns results in the same
// shape as the LLM path. Used when no AI provider is configured
// and when the daily budget is exhausted, so the triage UI is
// never empty.
func deterministicFallback(ctx context.Context, deps TriageDeps, wsID uint32, limit int) ([]ai.InboxTriageSuggestion, error) {
	rows, err := deps.Queries.ListInbox(ctx, generated.ListInboxParams{
		WorkspaceID: wsID,
		Limit:       int32(limit),
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	signals := make([]inboxtriage.Signal, 0, len(rows))
	for _, r := range rows {
		signals = append(signals, inboxtriage.Signal{
			InboxItemID: r.PublicID.String(),
			Source:      string(r.Source),
			Kind:        r.Kind,
			ReceivedAt:  r.ReceivedAt,
			HasTask:     r.TaskPublicID.Valid && r.TaskPublicID.String != "",
			Now:         now,
		})
	}
	results := inboxtriage.Evaluate(signals)
	out := make([]ai.InboxTriageSuggestion, 0, len(results))
	for _, r := range results {
		out = append(out, ai.InboxTriageSuggestion{
			InboxItemID:       r.InboxItemID,
			Score:             r.Score,
			RecommendedAction: string(r.RecommendedAction),
			Reasoning:         r.Reasoning,
		})
	}
	return out, nil
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
		limit := in.Body.Limit
		if limit <= 0 {
			limit = 20
		}
		if limit > 50 {
			limit = 50
		}

		// Try the LLM-backed path first. On ErrNoProvider or
		// ErrDailyBudgetExceeded we fall back to the deterministic
		// rule engine (2.AI-7) so the triage UI is never empty.
		// Other errors (parse failures, upstream errors) bubble up
		// as before — those indicate a misconfigured provider, not
		// the "no LLM" case.
		var suggestions []ai.InboxTriageSuggestion
		if deps.AI != nil {
			suggestions, err = deps.AI.ProposeInboxTriage(ctx, wsID, limit)
			if err != nil {
				switch {
				case errors.Is(err, ai.ErrNoProvider), errors.Is(err, ai.ErrDailyBudgetExceeded):
					suggestions, err = deterministicFallback(ctx, deps, wsID, limit)
				case errors.Is(err, ai.ErrParse):
					return nil, httpErr(apierrors.AiResponseParseFailed)
				default:
					return nil, httpErr(apierrors.AiProviderUpstreamCallFailed)
				}
			}
		} else {
			suggestions, err = deterministicFallback(ctx, deps, wsID, limit)
		}
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
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
