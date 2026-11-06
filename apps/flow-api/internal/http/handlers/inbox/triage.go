// Package inbox - triage.go is the endpoint that asks
// the AI orchestrator to score the workspace inbox and return
// per-item recommended actions. The route is registered separately
// from the v1 inbox routes (Register) because it needs the orchestrator
// dependency and lives behind the workspace-scoped router group.
package inbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/inboxtriage"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/errormodel"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/resolve"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/suggestionprivacy"
)

// TriageDeps is the dependency bundle for the triage endpoint. It is a
// separate struct from Deps because the v1 inbox routes do not need an
// AI orchestrator and we want to keep handler wiring narrow.
type TriageDeps struct {
	Deps
	AI                *ai.Orchestrator
	SuggestionPrivacy *suggestionprivacy.Store
}

// TriageSuggestion is the per-item DTO returned to the client.
type TriageSuggestion struct {
	InboxItemID       string  `json:"inboxItemId"`
	Score             float32 `json:"score"`
	RecommendedAction string  `json:"recommendedAction"`
	Reasoning         string  `json:"reasoning"`
}

// TriageInputBody is the JSON body for POST /workspaces/{wsId}/inbox/triage.
type TriageInputBody struct {
	Limit int `json:"limit,omitempty" minimum:"1" maximum:"50" doc:"Number of inbox items to score (default 20, max 50)"`
}

// TriageInput is the request for POST /workspaces/{wsId}/inbox/triage.
type TriageInput struct {
	WorkspaceID string `path:"wsId"`
	Body        TriageInputBody
}

// TriageOutputBody is the response body for POST /workspaces/{wsId}/inbox/triage.
type TriageOutputBody struct {
	Suggestions []TriageSuggestion `json:"suggestions"`
}

// TriageOutput is the response envelope for the triage endpoint.
type TriageOutput struct {
	Body TriageOutputBody
}

// deterministicFallback runs the pure inboxtriage rule engine over
// the workspace's current inbox and returns results in the same
// shape as the LLM path. Used when no AI provider is configured
// and when the daily budget is exhausted, so the triage UI is
// never empty.
func deterministicFallback(ctx context.Context, deps TriageDeps, wsID uint32, vis acl.VisibilityArgs, limit int) ([]ai.InboxTriageSuggestion, map[string]string, []types.PublicID, error) {
	rows, err := deps.Queries.ListInbox(ctx, generated.ListInboxParams{
		WorkspaceID: wsID,
		IsElevated:  vis.IsElevated,
		ActorUserID: vis.ActorUserID,
		Limit:       int32(limit), //#nosec G115 -- limit is validated to maximum:50 by the handler
		Offset:      0,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	sourceVersions, err := ai.CaptureInboxTriageSourceVersions(rows)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("inbox: capture deterministic triage evidence: %w", err)
	}
	taskRefs, err := ai.CaptureInboxTriageTaskRefs(rows)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("inbox: capture deterministic triage task refs: %w", err)
	}
	now := time.Now().UTC()
	signals := make([]inboxtriage.Signal, 0, len(rows))
	for _, r := range rows {
		signals = append(signals, inboxtriage.Signal{
			InboxItemID: r.PublicID.String(),
			Source:      string(r.Source),
			Kind:        r.Kind,
			ReceivedAt:  r.ReceivedAt,
			HasTask:     r.TaskVisible.Valid && r.TaskVisible.Bool && r.TaskPublicID.Valid && r.TaskPublicID.String != "",
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
	return out, sourceVersions, taskRefs, nil
}

// persistDeterministicSuggestions gives fallback results a durable proposal
// identity and exact source version just like model-backed results. Without
// this event the new command endpoint could not safely hydrate the action
// returned to an inbox user.
func persistDeterministicSuggestions(ctx context.Context, deps TriageDeps, wsID, actorID uint32, suggestions []ai.InboxTriageSuggestion, sourceVersions map[string]string, taskRefs []types.PublicID) error {
	if deps.DB == nil {
		return nil
	}
	if len(suggestions) == 0 {
		return nil
	}
	if deps.SuggestionPrivacy == nil || actorID == 0 {
		return fmt.Errorf("inbox: suggestion privacy is not configured for the authenticated actor")
	}
	refs := make([]suggestionprivacy.Ref, 0, len(taskRefs))
	for _, taskID := range taskRefs {
		refs = append(refs, suggestionprivacy.Ref{Kind: suggestionprivacy.ResourceTask, PublicID: taskID})
	}
	batchID, err := deps.SuggestionPrivacy.Create(ctx, wsID, actorID, refs)
	if err != nil {
		return fmt.Errorf("inbox: persist deterministic suggestion provenance: %w", err)
	}
	if err := deps.SuggestionPrivacy.Authorize(ctx, generated.New(deps.DB), wsID, actorID, batchID); err != nil {
		return fmt.Errorf("inbox: deterministic suggestion input is no longer readable: %w", err)
	}
	return dbretry.InTx(ctx, deps.DB, "inbox.PersistDeterministicSuggestions", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		for _, suggestion := range suggestions {
			sourceVersion, ok := sourceVersions[suggestion.InboxItemID]
			if !ok {
				continue
			}
			if err := deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: wsID}, mutationlog.Mutation{
				EventType:    eventbus.AiSuggestionProposed,
				AuditAction:  "inbox.ai_propose",
				ResourceType: "signal",
				ResourceID:   suggestion.InboxItemID,
				Payload: map[string]any{
					"inbox_item_id":       suggestion.InboxItemID,
					"score":               suggestion.Score,
					"action":              suggestion.RecommendedAction,
					"reasoning":           suggestion.Reasoning,
					"source_version":      sourceVersion,
					"suggestion_batch_id": batchID.String(),
				},
				CallSite: "inbox.PersistDeterministicSuggestions",
			}); err != nil {
				return err
			}
		}
		return nil
	})
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
		Description: "Synchronously runs the AI triage prompt over the caller's pending inbox items and returns a per-item score plus recommended action. Each call is recorded as an ai.suggestion.proposed event so /ai/suggestions can replay it.",
		Tags:        []string{"Tasks"},
	}, Triage(deps))
}

// Triage handles POST /workspaces/{wsId}/inbox/triage.
func Triage(deps TriageDeps) func(context.Context, *TriageInput) (*TriageOutput, error) {
	return func(ctx context.Context, in *TriageInput) (*TriageOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsID, err := resolve.WorkspaceMember(ctx, deps.DB, in.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}
		vis, err := readerVisibility(ctx, deps, wsID, actorID)
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

		// No provider or no budget falls back to the rule engine; any other model error surfaces.
		var suggestions []ai.InboxTriageSuggestion
		var sourceVersions map[string]string
		var taskRefs []types.PublicID
		usedFallback := false
		if deps.AI != nil {
			suggestions, err = deps.AI.ProposeInboxTriage(ctx, wsID, vis, limit)
			if err != nil {
				switch {
				case errors.Is(err, ai.ErrNoProvider), errors.Is(err, providers.ErrNoEnabledProvider), errors.Is(err, ai.ErrDailyBudgetExceeded):
					suggestions, sourceVersions, taskRefs, err = deterministicFallback(ctx, deps, wsID, vis, limit)
					usedFallback = true
				default:
					return nil, handlerutil.HTTPErrWithRetryAfter(errormodel.AIFailure(err))
				}
			}
		} else {
			suggestions, sourceVersions, taskRefs, err = deterministicFallback(ctx, deps, wsID, vis, limit)
			usedFallback = true
		}
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if usedFallback {
			if err := persistDeterministicSuggestions(ctx, deps, wsID, actorID, suggestions, sourceVersions, taskRefs); err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}
		out := &TriageOutput{}
		out.Body.Suggestions = make([]TriageSuggestion, 0, len(suggestions))
		for _, s := range suggestions {
			out.Body.Suggestions = append(out.Body.Suggestions, TriageSuggestion{
				InboxItemID:       s.InboxItemID,
				Score:             s.Score,
				RecommendedAction: s.RecommendedAction,
				Reasoning:         s.Reasoning,
			})
		}
		return out, nil
	}
}
