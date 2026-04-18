package relations

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// ListForWorkspace handles GET /workspaces/{wsId}/relation-suggestions.
// Returns pending relation suggestions for the workspace with pagination.
func ListForWorkspace(deps Deps) func(context.Context, *ListForWorkspaceInput) (*ListForWorkspaceOutput, error) {
	return func(ctx context.Context, in *ListForWorkspaceInput) (*ListForWorkspaceOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListPendingSuggestionsForWorkspace(ctx, generated.ListPendingSuggestionsForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListForWorkspaceOutput{}
		out.Body.Suggestions = make([]SuggestionDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Suggestions = append(out.Body.Suggestions, mapWorkspaceRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// ListForTask handles GET /tasks/{taskId}/relation-suggestions.
// Returns pending relation suggestions where the task is either the
// source or target.
func ListForTask(deps Deps) func(context.Context, *ListForTaskInput) (*ListForTaskOutput, error) {
	return func(ctx context.Context, in *ListForTaskInput) (*ListForTaskOutput, error) {
		tc, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListPendingSuggestionsForTask(ctx, generated.ListPendingSuggestionsForTaskParams{
			SourceTaskID: tc.ID,
			TargetTaskID: tc.ID,
			Limit:        limit,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListForTaskOutput{}
		out.Body.Suggestions = make([]SuggestionDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Suggestions = append(out.Body.Suggestions, mapTaskRow(r))
		}
		return out, nil
	}
}

// Resolve handles POST /relation-suggestions/{suggestionId}/resolve.
// Accepts or dismisses a pending suggestion. On accept, creates a
// task_dependencies row and emits RelationAccepted. On dismiss, updates
// the status and emits RelationDismissed.
func Resolve(deps Deps) func(context.Context, *ResolveInput) (*ResolveOutput, error) {
	return func(ctx context.Context, in *ResolveInput) (*ResolveOutput, error) {
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

		pub, err := types.Parse(in.SuggestionID)
		if err != nil {
			return nil, httpErr(apierrors.RelationSuggestionNotFound)
		}

		// We need workspace context to scope the query. The suggestion
		// itself carries the workspace_id, so we look it up across all
		// workspaces the actor belongs to. We first need to resolve the
		// suggestion; we'll iterate the actor's workspaces if needed.
		// For simplicity, we query using the raw DB to find the
		// suggestion's workspace, then verify membership.
		var wsID uint32
		const wsFindQuery = `SELECT rs.workspace_id FROM relation_suggestions rs
INNER JOIN workspace_members wm ON wm.workspace_id = rs.workspace_id AND wm.user_id = ? AND wm.enabled = TRUE
WHERE rs.public_id = ?
LIMIT 1`
		if err := deps.DB.QueryRowContext(ctx, wsFindQuery, actorID, pub).Scan(&wsID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.RelationSuggestionNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		// Fetch the full suggestion row (scoped to the resolved workspace).
		suggestion, err := deps.Queries.GetSuggestionByPublicId(ctx, generated.GetSuggestionByPublicIdParams{
			WorkspaceID: wsID,
			PublicID:    pub,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.RelationSuggestionNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if suggestion.Status != generated.RelationSuggestionsStatusPending {
			return nil, httpErr(apierrors.RelationSuggestionAlreadyResolved)
		}

		switch in.Body.Action {
		case "accept":
			return resolveAccept(ctx, deps, suggestion, wsID, actorID)
		case "dismiss":
			return resolveDismiss(ctx, deps, suggestion, wsID, actorID)
		default:
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
	}
}

// resolveAccept creates a task_dependencies row and marks the suggestion
// as accepted.
func resolveAccept(
	ctx context.Context,
	deps Deps,
	suggestion generated.GetSuggestionByPublicIdRow,
	wsID uint32,
	actorID uint32,
) (*ResolveOutput, error) {
	// Map the suggestion kind to a task_dependencies kind.
	depKind, err := mapSuggestionKindToDependencyKind(suggestion.SuggestedKind)
	if err != nil {
		return nil, httpErr(apierrors.InternalUnexpected)
	}

	// Create the dependency edge.
	depPub := types.New()
	if _, err := deps.Queries.AddDependency(ctx, generated.AddDependencyParams{
		PublicID:    depPub,
		WorkspaceID: wsID,
		FromTaskID:  suggestion.SourceTaskID,
		ToTaskID:    suggestion.TargetTaskID,
		Kind:        depKind,
	}); err != nil {
		// Duplicate edge is acceptable; the dependency already exists.
		if !isDuplicateEntry(err) {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
	}

	// Mark the suggestion as accepted.
	if err := deps.Queries.ResolveSuggestion(ctx, generated.ResolveSuggestionParams{
		Status:      generated.RelationSuggestionsStatusAccepted,
		ResolvedBy:  sql.NullInt32{Int32: int32(actorID), Valid: true},
		WorkspaceID: wsID,
		PublicID:    suggestion.PublicID,
	}); err != nil {
		return nil, httpErr(apierrors.InternalUnexpected)
	}

	// Emit event.
	actor := int64(actorID)
	srcTaskID := int64(suggestion.SourceTaskID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.RelationAccepted,
		WorkspaceID: wsID,
		ActorUserID: &actor,
		TaskID:      &srcTaskID,
		Payload: map[string]any{
			"suggestionId":  suggestion.PublicID.String(),
			"suggestedKind": string(suggestion.SuggestedKind),
			"sourceTaskId":  suggestion.SourceTaskPublicID.String(),
			"targetTaskId":  suggestion.TargetTaskPublicID.String(),
		},
	})

	deps.Audit.Record(ctx, audit.Entry{
		Action:       "relation.suggestion.accept",
		ActorID:      actorID,
		WorkspaceID:  wsID,
		ResourceType: "relation_suggestion",
		ResourceID:   suggestion.PublicID.String(),
	})

	out := &ResolveOutput{}
	out.Body.Ok = true
	return out, nil
}

// resolveDismiss marks the suggestion as dismissed.
func resolveDismiss(
	ctx context.Context,
	deps Deps,
	suggestion generated.GetSuggestionByPublicIdRow,
	wsID uint32,
	actorID uint32,
) (*ResolveOutput, error) {
	if err := deps.Queries.ResolveSuggestion(ctx, generated.ResolveSuggestionParams{
		Status:      generated.RelationSuggestionsStatusDismissed,
		ResolvedBy:  sql.NullInt32{Int32: int32(actorID), Valid: true},
		WorkspaceID: wsID,
		PublicID:    suggestion.PublicID,
	}); err != nil {
		return nil, httpErr(apierrors.InternalUnexpected)
	}

	// Emit event.
	actor := int64(actorID)
	srcTaskID := int64(suggestion.SourceTaskID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.RelationDismissed,
		WorkspaceID: wsID,
		ActorUserID: &actor,
		TaskID:      &srcTaskID,
		Payload: map[string]any{
			"suggestionId":  suggestion.PublicID.String(),
			"suggestedKind": string(suggestion.SuggestedKind),
			"sourceTaskId":  suggestion.SourceTaskPublicID.String(),
			"targetTaskId":  suggestion.TargetTaskPublicID.String(),
		},
	})

	deps.Audit.Record(ctx, audit.Entry{
		Action:       "relation.suggestion.dismiss",
		ActorID:      actorID,
		WorkspaceID:  wsID,
		ResourceType: "relation_suggestion",
		ResourceID:   suggestion.PublicID.String(),
	})

	out := &ResolveOutput{}
	out.Body.Ok = true
	return out, nil
}

// mapSuggestionKindToDependencyKind converts a relation_suggestions
// suggested_kind enum value to the corresponding task_dependencies
// kind enum value.
func mapSuggestionKindToDependencyKind(kind generated.RelationSuggestionsSuggestedKind) (generated.TaskDependenciesKind, error) {
	switch kind {
	case generated.RelationSuggestionsSuggestedKindBlocks:
		return generated.TaskDependenciesKindBlocks, nil
	case generated.RelationSuggestionsSuggestedKindRelates:
		return generated.TaskDependenciesKindRelates, nil
	case generated.RelationSuggestionsSuggestedKindDuplicates:
		return generated.TaskDependenciesKindDuplicates, nil
	default:
		return "", errors.New("unknown suggestion kind")
	}
}

// mapWorkspaceRow converts a ListPendingSuggestionsForWorkspaceRow to
// a SuggestionDTO.
func mapWorkspaceRow(r generated.ListPendingSuggestionsForWorkspaceRow) SuggestionDTO {
	return SuggestionDTO{
		ID:              r.PublicID.String(),
		SuggestedKind:   string(r.SuggestedKind),
		Confidence:      parseConfidence(r.Confidence),
		Status:          string(r.Status),
		SourceTaskID:    r.SourceTaskPublicID.String(),
		SourceTaskTitle: r.SourceTaskTitle,
		TargetTaskID:    r.TargetTaskPublicID.String(),
		TargetTaskTitle: r.TargetTaskTitle,
		CreatedAt:       r.CreatedAt.Unix(),
	}
}

// mapTaskRow converts a ListPendingSuggestionsForTaskRow to a
// SuggestionDTO.
func mapTaskRow(r generated.ListPendingSuggestionsForTaskRow) SuggestionDTO {
	return SuggestionDTO{
		ID:              r.PublicID.String(),
		SuggestedKind:   string(r.SuggestedKind),
		Confidence:      parseConfidence(r.Confidence),
		Status:          string(r.Status),
		SourceTaskID:    r.SourceTaskPublicID.String(),
		SourceTaskTitle: r.SourceTaskTitle,
		TargetTaskID:    r.TargetTaskPublicID.String(),
		TargetTaskTitle: r.TargetTaskTitle,
		CreatedAt:       r.CreatedAt.Unix(),
	}
}

// parseConfidence converts the DECIMAL(5,4) string from the DB into a
// float64.
func parseConfidence(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64

// isDuplicateEntry delegates to handlerutil.IsDuplicateEntry.
var isDuplicateEntry = handlerutil.IsDuplicateEntry
