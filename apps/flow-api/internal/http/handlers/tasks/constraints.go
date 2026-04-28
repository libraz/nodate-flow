package tasks

import (
	"context"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	nflog "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/log"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
)

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
		pub := types.New()
		if _, err := deps.Queries.AddConstraint(ctx, generated.AddConstraintParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
			Kind:        generated.TaskConstraintsKind(in.Body.Kind),
			Expression:  in.Body.Expression,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskConstraintAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"constraintId": pub.String(),
				"kind":         in.Body.Kind,
			},
		}); err != nil {
			nflog.LoggerFromContext(ctx).ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.AddConstraint"),
				slog.String("event_type", string(eventbus.TaskConstraintAdded)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("constraint_public_id", pub.String()),
			)
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
		if err := deps.Queries.DeleteConstraint(ctx, generated.DeleteConstraintParams{
			WorkspaceID: ws.ID,
			PublicID:    cid,
		}); err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsTaskNotFound, apierrors.InternalUnexpected))
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
