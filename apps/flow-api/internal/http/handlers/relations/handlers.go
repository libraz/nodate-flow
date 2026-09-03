package relations

import (
	"context"
	"database/sql"
	stderrors "errors"
	"strconv"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskdeps"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
)

// ListForWorkspace handles GET /workspaces/{wsId}/relation-suggestions.
// Returns pending relation suggestions for the workspace with pagination.
func ListForWorkspace(deps Deps) func(context.Context, *ListForWorkspaceInput) (*ListForWorkspaceOutput, error) {
	return func(ctx context.Context, in *ListForWorkspaceInput) (*ListForWorkspaceOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		// Both task titles ride on every suggestion row, so the list has
		// to be filtered to suggestions whose two ends the actor may
		// both see. COUNT(*) OVER() sits inside the same statement, so
		// total counts the filtered set — a total taken before the
		// filter would report rows the caller is told nothing about,
		// which is its own disclosure.
		vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(ws.Role))
		rows, err := deps.Queries.ListPendingSuggestionsForWorkspace(ctx, generated.ListPendingSuggestionsForWorkspaceParams{
			WorkspaceID:   ws.ID,
			IsElevated:    vis.IsElevated,
			ActorUserID:   vis.ActorUserID,
			ActorUserID_2: vis.ActorUserID,
			ActorUserID_3: vis.ActorUserID,
			ActorUserID_4: vis.ActorUserID,
			ActorUserID_5: vis.ActorUserID,
			ActorUserID_6: vis.ActorUserID,
			Limit:         limit,
			Offset:        in.Offset,
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

		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceAccessDenied)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		// Reaching this route means the actor may see the task named in
		// the path; the other end of each suggestion is a different task
		// and is not covered by that. Same filter as the workspace list.
		vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(ws.Role))
		rows, err := deps.Queries.ListPendingSuggestionsForTask(ctx, generated.ListPendingSuggestionsForTaskParams{
			WorkspaceID:   ws.ID,
			SourceTaskID:  tc.ID,
			TargetTaskID:  tc.ID,
			IsElevated:    vis.IsElevated,
			ActorUserID:   vis.ActorUserID,
			ActorUserID_2: vis.ActorUserID,
			ActorUserID_3: vis.ActorUserID,
			ActorUserID_4: vis.ActorUserID,
			ActorUserID_5: vis.ActorUserID,
			ActorUserID_6: vis.ActorUserID,
			Limit:         limit,
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

		wsID, wsRole, err := resolveSuggestionWorkspaceForActor(ctx, deps, actorID, pub)
		if err != nil {
			return nil, err
		}

		// Fetch the full suggestion row (scoped to the resolved
		// workspace). Both task titles ride on the row, so the same
		// two-ended filter the list queries carry applies here: a
		// suggestion whose ends the actor may not both see reads as
		// absent rather than as a refusal.
		vis := acl.ListVisibilityArgs(actorID, wsRole)
		suggestion, err := deps.Queries.GetSuggestionByPublicId(ctx, generated.GetSuggestionByPublicIdParams{
			WorkspaceID:   wsID,
			PublicID:      pub,
			IsElevated:    vis.IsElevated,
			ActorUserID:   vis.ActorUserID,
			ActorUserID_2: vis.ActorUserID,
			ActorUserID_3: vis.ActorUserID,
			ActorUserID_4: vis.ActorUserID,
			ActorUserID_5: vis.ActorUserID,
			ActorUserID_6: vis.ActorUserID,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.RelationSuggestionNotFound, apierrors.InternalUnexpected))
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

// resolveSuggestionWorkspaceForActor returns the workspace a relation
// suggestion belongs to together with the caller's role in it, and only
// when the caller is an enabled member of that workspace.
//
// The route carries no workspace parameter, so the workspace has to come from
// the suggestion itself — which means the lookup is also the access check.
// The membership join is part of the same statement for that reason: a
// two-step version can resolve the workspace and then forget to ask whether
// the caller belongs to it, and the answer would be a suggestion from someone
// else's workspace. A suggestion the caller cannot reach is reported as
// absent rather than refused, so the id space stays opaque.
//
// The role comes back from the same row because the caller needs it to
// decide the Layer-4 elevation flag, and a second membership lookup is a
// second chance for the two answers to disagree.
func resolveSuggestionWorkspaceForActor(
	ctx context.Context,
	deps Deps,
	actorID uint32,
	pub types.PublicID,
) (uint32, acl.WorkspaceRole, error) {
	const query = `SELECT rs.workspace_id, wm.role FROM relation_suggestions rs
INNER JOIN workspace_members wm ON wm.workspace_id = rs.workspace_id AND wm.user_id = ? AND wm.enabled = TRUE
WHERE rs.public_id = ?
LIMIT 1`
	var wsID uint32
	var role string
	if err := deps.DB.QueryRowContext(ctx, query, actorID, pub).Scan(&wsID, &role); err != nil {
		return 0, "", httpErr(apierr.SpecForErrNoRows(err, apierrors.RelationSuggestionNotFound, apierrors.InternalUnexpected))
	}
	wsRole := acl.WorkspaceRole(role)
	if !wsRole.IsValid() {
		// A role the Go enum does not know is a corrupt row, not a
		// permission answer; failing closed here would read as a
		// missing suggestion and hide the invariant violation.
		return 0, "", httpErr(apierrors.InternalUnexpected)
	}
	return wsID, wsRole, nil
}

// resolveAccept creates a task_dependencies row and marks the suggestion
// as accepted.
//
// Accepting a suggestion writes a real dependency edge, so it is held to
// exactly what POST /tasks/{id}/dependencies is held to: the caller must
// be able to reach both endpoint tasks and be at least an editor in both
// their projects, and the edge goes in through [taskdeps.Add] so it gets
// the workspace edge lock and the cycle rejection. Workspace membership alone
// is not enough — it would let a guest draw edges between tasks in
// projects they were never added to — and skipping the cycle check
// breaks the DAG the `dependency.all_done` walk depends on, silently and
// permanently.
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

	// Called for the authorization it performs, not for the row: the
	// edge is written by internal task id and the projects are no longer
	// part of the lock set.
	if _, err := authorizeEndpoint(ctx, deps, suggestion.SourceTaskPublicID, actorID); err != nil {
		return nil, err
	}
	if _, err := authorizeEndpoint(ctx, deps, suggestion.TargetTaskPublicID, actorID); err != nil {
		return nil, err
	}

	actor := int64(actorID)
	txErr := dbretry.InTx(ctx, deps.DB, "relations.Accept", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, e := taskdeps.Add(ctx, tx, taskdeps.Args{
			WorkspaceID:      wsID,
			FromTaskID:       suggestion.SourceTaskID,
			ToTaskID:         suggestion.TargetTaskID,
			Kind:             depKind,
			ActorUserID:      &actor,
			FromTaskPublicID: suggestion.SourceTaskPublicID.String(),
			ToTaskPublicID:   suggestion.TargetTaskPublicID.String(),
			Via:              "relation.accept",
		}); e != nil {
			// An edge somebody already drew is not a reason to refuse
			// the suggestion: the state the caller asked for holds.
			if !stderrors.Is(e, taskdeps.ErrDuplicate) {
				return e
			}
		}

		// Mark the suggestion as accepted in the same transaction as the
		// edge, so a failure cannot leave a suggestion claiming an edge
		// that was never written.
		// The count is not the existence check: the suggestion was read
		// before this call, and re-accepting one already accepted changes
		// no column, which MySQL counts as zero.
		if _, e := deps.Queries.WithTx(tx.RawTx()).ResolveSuggestion(ctx, generated.ResolveSuggestionParams{
			Status:      generated.RelationSuggestionsStatusAccepted,
			ResolvedBy:  sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
			WorkspaceID: wsID,
			PublicID:    suggestion.PublicID,
		}); e != nil {
			return e
		}

		// The event goes in with the edge and the status, not after them.
		// Everything it says is known before the transaction opens, so
		// nothing here needs a row that does not exist yet — the boundary
		// sat outside only because that is where the append happened to
		// be written. Inside, a failed append rolls the accept back
		// instead of leaving an accepted suggestion nothing was told
		// about, and the fan-out still waits for the commit because the
		// transaction is the boundary it defers to.
		srcTaskID := int64(suggestion.SourceTaskID)
		return eventbus.Append(ctx, tx, eventbus.Event{
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
	})
	if txErr != nil {
		if stderrors.Is(txErr, taskdeps.ErrCycle) {
			return nil, httpErr(apierrors.WsTaskDependencyCycle)
		}
		return nil, httpErr(apierrors.InternalUnexpected)
	}

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
	// Not an existence check: resolving a suggestion that already holds
	// the target status changes nothing and MySQL counts zero.
	if _, err := deps.Queries.ResolveSuggestion(ctx, generated.ResolveSuggestionParams{
		Status:      generated.RelationSuggestionsStatusDismissed,
		ResolvedBy:  sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
		WorkspaceID: wsID,
		PublicID:    suggestion.PublicID,
	}); err != nil {
		return nil, httpErr(apierrors.InternalUnexpected)
	}

	// Emit event.
	actor := int64(actorID)
	srcTaskID := int64(suggestion.SourceTaskID)
	eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
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
	}, "relations.resolveDismiss")

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

// authorizeEndpoint resolves one endpoint task of a suggestion and
// applies the same floor POST /tasks/{id}/dependencies applies: the
// caller must be able to see the task at all (workspace membership plus
// task visibility) and be at least an editor in the project that owns
// it. Elevated project roles pass regardless, matching the MCP and REST
// resolvers.
//
// Errors from the ACL layer already carry the canonical spec, so they
// travel back unchanged rather than being flattened into a 500.
func authorizeEndpoint(ctx context.Context, deps Deps, taskPub types.PublicID, actorID uint32) (acl.TaskAccess, error) {
	access, err := acl.AuthorizeTaskAccess(ctx, deps.DB, taskPub.UUID(), actorID)
	if err != nil {
		return acl.TaskAccess{}, err
	}
	if access.ProjectRole != acl.ProjectRoleElevated && !access.ProjectRole.AtLeast(acl.ProjectRoleEditor) {
		return acl.TaskAccess{}, httpErr(apierrors.WsProjectAccessDenied)
	}
	return access, nil
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
		return "", apierrors.Newf(apierrors.InternalUnexpected, "unknown suggestion kind %q", kind)
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
