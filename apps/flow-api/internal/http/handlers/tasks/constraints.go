package tasks

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/libraz/nodate-flow/apps/flow-api/internal/log"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
)

// constraintParseErr maps a constraint.Parse failure to the matching
// CONSTRAINT.PARSE.* apierror spec so the HTTP layer returns the precise
// 422 code (and i18n key) for the offending DSL node.
func constraintParseErr(err error) *apierrors.Spec {
	var pe *constraint.ParseError
	if errors.As(err, &pe) {
		switch pe.Code {
		case constraint.CodeUnsupportedOperator:
			return apierrors.ConstraintParseUnsupportedOperator
		case constraint.CodeMissingArg:
			return apierrors.ConstraintParseMissingArg
		case constraint.CodeEmptyTerms:
			return apierrors.ConstraintParseEmptyTerms
		case constraint.CodeInvalidJSON:
			return apierrors.ConstraintParseInvalidJson
		}
	}
	return apierrors.ConstraintParseInvalidJson
}

// AddConstraint handles POST /tasks/{id}/constraints.
func AddConstraint(deps Deps) func(context.Context, *AddTaskConstraintInput) (*AddTaskConstraintOutput, error) {
	return func(ctx context.Context, in *AddTaskConstraintInput) (*AddTaskConstraintOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		// Validate the DSL expression up front so an unparseable constraint
		// is rejected at add time instead of saving with HTTP 200 and being
		// silently inert (autoEvaluateConstraints swallows parse errors).
		// Mirrors the NL constraint compile/explain handlers.
		if _, err := constraint.Parse([]byte(in.Body.Expression)); err != nil {
			return nil, httpErr(constraintParseErr(err))
		}
		pub := types.New()
		taskInternal := int64(task.ID)
		// Insert and append atomically so a crash between the insert and a
		// post-commit append cannot lose the timeline row (L-14).
		if err := dbretry.InTx(ctx, deps.DB, "tasks.AddConstraint", nil, func(ctx context.Context, tx *sql.Tx) error {
			qtx := deps.Queries.WithTx(tx)
			if _, e := qtx.AddConstraint(ctx, generated.AddConstraintParams{
				PublicID:    pub,
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				Kind:        generated.TaskConstraintsKind(in.Body.Kind),
				Expression:  in.Body.Expression,
			}); e != nil {
				return e
			}
			return eventbus.Append(ctx, tx, eventbus.Event{
				Type:        eventbus.TaskConstraintAdded,
				WorkspaceID: ws.ID,
				ActorUserID: actorPtr(ctx),
				TaskID:      &taskInternal,
				Payload: map[string]any{
					"taskId":       task.PublicID.String(),
					"constraintId": pub.String(),
					"kind":         in.Body.Kind,
				},
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if aID, aOk := middleware.ActorFromContext(ctx); aOk {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.constraint.add",
				ActorID:      aID,
				WorkspaceID:  ws.ID,
				ResourceType: "task_constraint",
				ResourceID:   pub.String(),
			})
		}
		autoEvaluateConstraints(ctx, deps, ws.ID, task.ID)
		return &AddTaskConstraintOutput{Body: TaskConstraint{
			ID:         pub.String(),
			Kind:       in.Body.Kind,
			Expression: in.Body.Expression,
		}}, nil
	}
}

// RemoveConstraint handles DELETE /tasks/{id}/constraints/{cid}.
func RemoveConstraint(deps Deps) func(context.Context, *RemoveTaskConstraintInput) (*RemoveTaskConstraintOutput, error) {
	return func(ctx context.Context, in *RemoveTaskConstraintInput) (*RemoveTaskConstraintOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		cid, err := types.Parse(in.CID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		affected, err := deps.Queries.DeleteConstraint(ctx, generated.DeleteConstraintParams{
			WorkspaceID: ws.ID,
			PublicID:    cid,
			TaskID:      task.ID,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		// 0 rows means the constraint does not exist on this task's path (it
		// may belong to a sibling task). Return NOT_FOUND so a no-op is
		// distinguishable from a real delete and one task cannot delete
		// another task's constraint.
		if affected == 0 {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskConstraintRemoved,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"constraintId": cid.String(),
			},
		}); err != nil {
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.RemoveConstraint"),
				slog.String("event_type", string(eventbus.TaskConstraintRemoved)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("constraint_public_id", cid.String()),
			)
		}
		if aID, aOk := middleware.ActorFromContext(ctx); aOk {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.constraint.remove",
				ActorID:      aID,
				WorkspaceID:  ws.ID,
				ResourceType: "task_constraint",
				ResourceID:   cid.String(),
			})
		}
		autoEvaluateConstraints(ctx, deps, ws.ID, task.ID)
		out := &RemoveTaskConstraintOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
