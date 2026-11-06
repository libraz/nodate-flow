// Package inbox - suggestions_list.go exposes the persisted AI
// suggestion lifecycle so the Glass Dock can resume across devices. The lifecycle lives in the events table; this handler reads it
// back, and suggestion_commands.go records the reactions.
package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/resolve"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/suggestionprivacy"
)

// AiSuggestionSummary is the public DTO for one pending AI suggestion.
type AiSuggestionSummary struct {
	EventID             string  `json:"eventId"`
	ProposalID          string  `json:"proposalId"`
	InboxItemID         string  `json:"inboxItemId"`
	Revision            uint32  `json:"revision"`
	DecisionState       string  `json:"decisionState,omitempty"`
	DecisionExpiresAt   *int64  `json:"decisionExpiresAt,omitempty"`
	SourceVersionStatus string  `json:"sourceVersionStatus" enum:"captured,legacy"`
	Score               float32 `json:"score"`
	RecommendedAction   string  `json:"recommendedAction"`
	Reasoning           string  `json:"reasoning"`
	ProposedAt          int64   `json:"proposedAt"`
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

// suggestionPayload is the on-disk shape of an ai.suggestion.proposed
// payload_json. Field names match the snake_case keys written by
// ai.ProposeInboxTriage.
type suggestionPayload struct {
	InboxItemID       string  `json:"inboxItemId"`
	InboxItemIDSnake  string  `json:"inbox_item_id"`
	Score             float32 `json:"score"`
	Action            string  `json:"action"`
	RecommendedAction string  `json:"recommendedAction"`
	Reasoning         string  `json:"reasoning"`
	SourceVersion     string  `json:"source_version"`
	SourceVersionAlt  string  `json:"sourceVersion"`
	SuggestionBatchID string  `json:"suggestion_batch_id"`
}

// RegisterAiSuggestions wires the workspace-scoped AI suggestion routes:
// list pending, execute, and decide. The caller must attach RequireAuth +
// RequireWorkspaceMember on the chi group it hands in.
func RegisterAiSuggestions(api huma.API, deps TriageDeps) {
	RegisterSuggestionCommands(api, deps)
	huma.Register(api, huma.Operation{
		OperationID: "ai-suggestions-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/ai/suggestions",
		Summary:     "List pending AI suggestions for a workspace",
		Description: "Returns AI inbox triage suggestions that have been proposed but not yet applied or dismissed. Includes the persisted proposal identity and the caller's actor-scoped decision revision.",
		Tags:        []string{"Tasks"},
	}, ListAiSuggestions(deps))
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
		vis, err := readerVisibility(ctx, deps, wsID, actorID)
		if err != nil {
			return nil, err
		}
		if deps.SuggestionPrivacy == nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		rows, err := deps.Queries.ListEffectivePendingAiSuggestionsForReader(ctx, generated.ListEffectivePendingAiSuggestionsForReaderParams{
			WorkspaceID: wsID,
			IsElevated:  int64(vis.IsElevated),
			ActorUserID: vis.ActorUserID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &AiSuggestionListOutput{}
		out.Body.Suggestions = make([]AiSuggestionSummary, 0, len(rows))
		for _, r := range rows {
			var p suggestionPayload
			if err := json.Unmarshal(r.PayloadJson, &p); err != nil {
				slog.WarnContext(ctx, "ai suggestion: skip malformed payload",
					slog.String("eventId", r.ProposalPublicID.String()),
					slog.String("err", err.Error()))
				continue
			}
			// Batchless legacy proposals have no verifiable owner or complete
			// input set. Keep them out of the new actor-scoped review surface.
			if p.SuggestionBatchID == "" {
				continue
			}
			batchID, err := types.Parse(p.SuggestionBatchID)
			if err != nil {
				slog.WarnContext(ctx, "ai suggestion: skip proposal with malformed batch id",
					slog.String("eventId", r.ProposalPublicID.String()),
					slog.String("err", err.Error()))
				continue
			}
			if err := deps.SuggestionPrivacy.Authorize(ctx, generated.New(deps.DB), wsID, actorID, batchID); err != nil {
				if errors.Is(err, suggestionprivacy.ErrNotReadable) {
					continue
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			action := p.Action
			if action == "" {
				action = p.RecommendedAction
			}
			itemID := p.InboxItemID
			if itemID == "" {
				itemID = p.InboxItemIDSnake
			}
			if itemID == "" {
				itemID = r.InboxItemPublicID.String()
			}
			revision := uint32(0)
			if r.Revision.Valid && r.Revision.Int32 > 0 {
				revision = uint32(r.Revision.Int32)
			}
			state := ""
			reasoning := p.Reasoning
			decisionExpiresAt := (*int64)(nil)
			activeDecision := r.DecisionExpiresAt.Valid && r.DecisionExpiresAt.Time.After(time.Now().UTC())
			if r.DecisionExpiresAt.Valid {
				unix := r.DecisionExpiresAt.Time.Unix()
				decisionExpiresAt = &unix
			}
			if activeDecision && r.DecisionState.Valid {
				state = string(r.DecisionState.AiSuggestionDecisionsState)
				if state == string(generated.AiSuggestionDecisionsStateEdited) {
					if r.DecisionAction.Valid {
						action = string(r.DecisionAction.AiSuggestionDecisionsAction)
					}
					if r.DecisionReasoning.Valid {
						reasoning = r.DecisionReasoning.String
					}
				}
			}
			proposalID := r.ProposalPublicID.String()
			out.Body.Suggestions = append(out.Body.Suggestions, AiSuggestionSummary{
				EventID:             proposalID,
				ProposalID:          proposalID,
				InboxItemID:         itemID,
				Revision:            revision,
				DecisionState:       state,
				DecisionExpiresAt:   decisionExpiresAt,
				SourceVersionStatus: suggestionSourceVersionStatus(p),
				Score:               p.Score,
				RecommendedAction:   action,
				Reasoning:           reasoning,
				ProposedAt:          r.OccurredAt.Unix(),
			})
		}
		return out, nil
	}
}

func suggestionSourceVersionStatus(payload suggestionPayload) string {
	version := payload.SourceVersion
	if version == "" {
		version = payload.SourceVersionAlt
	}
	if len(version) != 64 || strings.Trim(version, "0123456789abcdefABCDEF") != "" {
		return "legacy"
	}
	return "captured"
}

// readerVisibility derives the Layer 4 binds for the caller in a
// workspace the handler resolved by path rather than through
// RequireWorkspaceMember, so the role is looked up here.
func readerVisibility(ctx context.Context, deps TriageDeps, wsID, actorID uint32) (acl.VisibilityArgs, error) {
	role, err := acl.CheckWorkspaceMember(ctx, deps.DB, wsID, actorID, apierrors.WsWorkspaceAccessDenied)
	if err != nil {
		return acl.VisibilityArgs{}, err
	}
	return acl.ListVisibilityArgs(actorID, role), nil
}
