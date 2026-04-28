// Package inbox - suggestions_list.go exposes the persisted AI
// suggestion lifecycle (proposed / applied / dismissed) so the Glass
// Dock can resume across devices. Per ADR 0002 the lifecycle lives in
// the events table; these handlers read it back and append the
// reaction events.
package inbox

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/resolve"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// AiSuggestionSummary is the public DTO for one pending AI suggestion.
type AiSuggestionSummary struct {
	EventID           string  `json:"eventId"`
	InboxItemID       string  `json:"inboxItemId"`
	Score             float32 `json:"score"`
	RecommendedAction string  `json:"recommendedAction"`
	Reasoning         string  `json:"reasoning"`
	ProposedAt        int64   `json:"proposedAt"`
}

// AiSuggestionListInput is the request for GET /workspaces/{wsId}/ai/suggestions.
type AiSuggestionListInput struct {
	WorkspaceID string `path:"wsId"`
}

// AiSuggestionListBody is the response body for GET /workspaces/{wsId}/ai/suggestions.
type AiSuggestionListBody struct {
	Suggestions []AiSuggestionSummary `json:"suggestions"`
}

// AiSuggestionListOutput is the response envelope for the list endpoint.
type AiSuggestionListOutput struct {
	Body AiSuggestionListBody
}

// AiSuggestionActionInput is the request shape for the apply / dismiss endpoints.
type AiSuggestionActionInput struct {
	WorkspaceID string `path:"wsId"`
	InboxItemID string `path:"inboxItemId"`
}

// AiSuggestionActionOutput is the empty response (204) for apply / dismiss.
type AiSuggestionActionOutput struct{}

// suggestionPayload is the on-disk shape of an ai.suggestion.proposed
// payload_json. Field names match the snake_case keys written by
// ai.ProposeInboxTriage.
type suggestionPayload struct {
	InboxItemID string  `json:"inbox_item_id"`
	Score       float32 `json:"score"`
	Action      string  `json:"action"`
	Reasoning   string  `json:"reasoning"`
}

// RegisterAiSuggestions wires the workspace-scoped AI suggestion routes:
// list pending, apply, dismiss. The caller must attach RequireAuth +
// RequireWorkspaceMember on the chi group it hands in.
func RegisterAiSuggestions(api huma.API, deps TriageDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "ai-suggestions-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/ai/suggestions",
		Summary:     "List pending AI suggestions for a workspace",
		Description: "Returns AI inbox triage suggestions that have been proposed but not yet applied or dismissed. Sourced from the events table per ADR 0002 so the Glass Dock can resume across devices.",
		Tags:        []string{"Tasks"},
	}, ListAiSuggestions(deps))
	huma.Register(api, huma.Operation{
		OperationID:   "ai-suggestions-apply",
		Method:        http.MethodPost,
		Path:          "/workspaces/{wsId}/ai/suggestions/{inboxItemId}/apply",
		Summary:       "Mark an AI suggestion as applied",
		Description:   "Appends an ai.suggestion.applied reaction event so the suggestion drops out of the pending list. The actual side-effect (e.g. archive or assign) is performed separately by the client; this endpoint only records the user's choice.",
		DefaultStatus: http.StatusNoContent,
		Tags:          []string{"Tasks"},
	}, ApplyAiSuggestion(deps))
	huma.Register(api, huma.Operation{
		OperationID:   "ai-suggestions-dismiss",
		Method:        http.MethodPost,
		Path:          "/workspaces/{wsId}/ai/suggestions/{inboxItemId}/dismiss",
		Summary:       "Dismiss an AI suggestion",
		Description:   "Appends an ai.suggestion.dismissed reaction event so the suggestion drops out of the pending list and the AI metrics counter records the negative signal.",
		DefaultStatus: http.StatusNoContent,
		Tags:          []string{"Tasks"},
	}, DismissAiSuggestion(deps))
}

// ListAiSuggestions handles GET /workspaces/{wsId}/ai/suggestions.
func ListAiSuggestions(deps TriageDeps) func(context.Context, *AiSuggestionListInput) (*AiSuggestionListOutput, error) {
	return func(ctx context.Context, in *AiSuggestionListInput) (*AiSuggestionListOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsID, err := resolve.WorkspaceMember(ctx, deps.DB, in.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}
		rows, err := deps.Queries.ListPendingAiSuggestions(ctx, wsID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &AiSuggestionListOutput{}
		out.Body.Suggestions = make([]AiSuggestionSummary, 0, len(rows))
		for _, r := range rows {
			var p suggestionPayload
			if err := json.Unmarshal(r.PayloadJson, &p); err != nil {
				slog.WarnContext(ctx, "ai suggestion: skip malformed payload",
					slog.String("eventId", r.PublicID.String()),
					slog.String("err", err.Error()))
				continue
			}
			out.Body.Suggestions = append(out.Body.Suggestions, AiSuggestionSummary{
				EventID:           r.PublicID.String(),
				InboxItemID:       p.InboxItemID,
				Score:             p.Score,
				RecommendedAction: p.Action,
				Reasoning:         p.Reasoning,
				ProposedAt:        r.OccurredAt.Unix(),
			})
		}
		return out, nil
	}
}

// ApplyAiSuggestion handles POST /workspaces/{wsId}/ai/suggestions/{inboxItemId}/apply.
func ApplyAiSuggestion(deps TriageDeps) func(context.Context, *AiSuggestionActionInput) (*AiSuggestionActionOutput, error) {
	return appendSuggestionReaction(deps, eventbus.AiSuggestionApplied)
}

// DismissAiSuggestion handles POST /workspaces/{wsId}/ai/suggestions/{inboxItemId}/dismiss.
func DismissAiSuggestion(deps TriageDeps) func(context.Context, *AiSuggestionActionInput) (*AiSuggestionActionOutput, error) {
	return appendSuggestionReaction(deps, eventbus.AiSuggestionDismissed)
}

func appendSuggestionReaction(deps TriageDeps, kind eventbus.Kind) func(context.Context, *AiSuggestionActionInput) (*AiSuggestionActionOutput, error) {
	return func(ctx context.Context, in *AiSuggestionActionInput) (*AiSuggestionActionOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		wsID, err := resolve.WorkspaceMember(ctx, deps.DB, in.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}
		if in.InboxItemID == "" {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
		actor := int64(actorID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        kind,
			WorkspaceID: wsID,
			ActorUserID: &actor,
			Payload: map[string]any{
				"inbox_item_id": in.InboxItemID,
			},
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &AiSuggestionActionOutput{}, nil
	}
}
