package inbox

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/resolve"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/libraz/nodate-flow/apps/flow-api/internal/log"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/suggestioncommand"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// SuggestionCommandInput names the workspace and inbox item whose persisted
// proposal is being acted on.
type SuggestionCommandInput struct {
	WorkspaceID string `path:"wsId"`
	InboxItemID string `path:"inboxItemId"`
	Body        SuggestionCommandBody
}

// SuggestionCommandBody contains the complete command identity and action.
// OperationKey is retained by the client across uncertain network outcomes.
type SuggestionCommandBody struct {
	OperationKey     string `json:"operationKey" minLength:"1" maxLength:"128"`
	ProposalID       string `json:"proposalId,omitempty"`
	ExpectedRevision uint32 `json:"expectedRevision" minimum:"0"`
	Action           string `json:"action" enum:"archive,snooze,open"`
	Reasoning        string `json:"reasoning,omitempty" maxLength:"2000"`
	SnoozeUntil      *int64 `json:"snoozeUntil,omitempty"`
}

// SuggestionCommandOutput is the atomic execution or navigation response.
type SuggestionCommandOutput struct {
	Body suggestioncommand.ExecuteResult
}

// SuggestionDecisionInput names the suggestion proposal and the revision the
// actor is updating.
type SuggestionDecisionInput struct {
	WorkspaceID string `path:"wsId"`
	InboxItemID string `path:"inboxItemId"`
	Body        SuggestionDecisionBody
}

// SuggestionDecisionBody is an actor-scoped edit, dismissal, or deferral.
type SuggestionDecisionBody struct {
	ProposalID       string `json:"proposalId"`
	ExpectedRevision uint32 `json:"expectedRevision" minimum:"0"`
	State            string `json:"state" enum:"edited,dismissed,deferred"`
	Action           string `json:"action,omitempty" enum:"archive,snooze,open"`
	Reasoning        string `json:"reasoning,omitempty" maxLength:"2000"`
	DeferUntil       *int64 `json:"deferUntil,omitempty"`
}

// SuggestionDecisionOutput is the persisted actor decision.
type SuggestionDecisionOutput struct {
	Body suggestioncommand.DecisionResult
}

// RegisterSuggestionCommands wires the atomic command and actor-decision
// endpoints.
func RegisterSuggestionCommands(api huma.API, deps TriageDeps) {
	huma.Register(api, huma.Operation{
		OperationID: "ai-suggestion-execute",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/ai/suggestions/{inboxItemId}/execute",
		Summary:     "Execute a persisted AI inbox suggestion",
		Description: "Atomically applies archive or snooze with its events and idempotency receipt. Open returns a currently authorized navigation target without resolving the suggestion.",
		Tags:        []string{"Tasks"},
	}, ExecuteSuggestion(deps))
	huma.Register(api, huma.Operation{
		OperationID: "ai-suggestion-decision",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/ai/suggestions/{inboxItemId}/decision",
		Summary:     "Save an actor-specific AI suggestion decision",
		Description: "Persists an edit, dismissal, or deferral for the selected proposal revision. Other workspace members keep their own decision state.",
		Tags:        []string{"Tasks"},
	}, DecideSuggestion(deps))
}

// ExecuteSuggestion handles POST /workspaces/{wsId}/ai/suggestions/{inboxItemId}/execute.
func ExecuteSuggestion(deps TriageDeps) func(context.Context, *SuggestionCommandInput) (*SuggestionCommandOutput, error) {
	return func(ctx context.Context, in *SuggestionCommandInput) (*SuggestionCommandOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		workspacePublicID, err := types.Parse(in.WorkspaceID)
		if err != nil {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		workspaceID, err := resolve.WorkspaceMember(ctx, deps.DB, in.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}
		inboxItemID, err := types.Parse(in.InboxItemID)
		if err != nil {
			return nil, httpErr(apierrors.WsInboxNotFound)
		}
		var proposalID *types.PublicID
		if in.Body.ProposalID != "" {
			parsed, err := types.Parse(in.Body.ProposalID)
			if err != nil {
				return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
			}
			proposalID = &parsed
		}
		result, err := (&suggestioncommand.Service{DB: deps.DB, Mutations: deps.Mutations, SuggestionPrivacy: deps.SuggestionPrivacy}).Execute(ctx, suggestioncommand.ExecuteCommand{
			WorkspaceID:       workspaceID,
			WorkspacePublicID: workspacePublicID,
			ActorUserID:       actorID,
			InboxItemID:       inboxItemID,
			OperationKey:      in.Body.OperationKey,
			ProposalID:        proposalID,
			ExpectedRevision:  in.Body.ExpectedRevision,
			Action:            in.Body.Action,
			Reasoning:         in.Body.Reasoning,
			SnoozeUntil:       in.Body.SnoozeUntil,
		})
		if err != nil {
			return nil, mapSuggestionCommandError(ctx, err)
		}
		return &SuggestionCommandOutput{Body: result}, nil
	}
}

// DecideSuggestion handles PATCH /workspaces/{wsId}/ai/suggestions/{inboxItemId}/decision.
func DecideSuggestion(deps TriageDeps) func(context.Context, *SuggestionDecisionInput) (*SuggestionDecisionOutput, error) {
	return func(ctx context.Context, in *SuggestionDecisionInput) (*SuggestionDecisionOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}
		workspacePublicID, err := types.Parse(in.WorkspaceID)
		if err != nil {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		workspaceID, err := resolve.WorkspaceMember(ctx, deps.DB, in.WorkspaceID, actorID)
		if err != nil {
			return nil, err
		}
		inboxItemID, err := types.Parse(in.InboxItemID)
		if err != nil {
			return nil, httpErr(apierrors.WsInboxNotFound)
		}
		proposalID, err := types.Parse(in.Body.ProposalID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
		}
		result, err := (&suggestioncommand.Service{DB: deps.DB, Mutations: deps.Mutations, SuggestionPrivacy: deps.SuggestionPrivacy}).Decide(ctx, suggestioncommand.DecisionCommand{
			WorkspaceID:       workspaceID,
			WorkspacePublicID: workspacePublicID,
			ActorUserID:       actorID,
			InboxItemID:       inboxItemID,
			ProposalID:        proposalID,
			ExpectedRevision:  in.Body.ExpectedRevision,
			State:             in.Body.State,
			Action:            in.Body.Action,
			Reasoning:         in.Body.Reasoning,
			DeferUntil:        in.Body.DeferUntil,
		})
		if err != nil {
			return nil, mapSuggestionCommandError(ctx, err)
		}
		return &SuggestionDecisionOutput{Body: result}, nil
	}
}

func mapSuggestionCommandError(ctx context.Context, err error) error {
	var apiErr *apierr.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Spec != nil && apiErr.Spec.Code == apierrors.InternalUnexpected.Code && apiErr.Cause != nil {
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "AI suggestion command failed", slog.String("error", apiErr.Cause.Error()))
		}
		return handlerutil.HTTPErrFromAPIError(apiErr)
	}
	return httpErr(apierrors.InternalUnexpected)
}
