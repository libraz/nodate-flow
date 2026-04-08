package tasks

import (
	"context"
	"database/sql"
	"errors"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// AddConstraint handles POST /tasks/{id}/constraints.
func AddConstraint(deps Deps) func(context.Context, *AddConstraintInput) (*AddConstraintOutput, error) {
	return func(ctx context.Context, in *AddConstraintInput) (*AddConstraintOutput, error) {
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
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        "task.constraint.added",
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"constraintId": pub.String(),
				"kind":         in.Body.Kind,
			},
		})
		return &AddConstraintOutput{Body: Constraint{
			ID:         pub.String(),
			Kind:       in.Body.Kind,
			Expression: in.Body.Expression,
		}}, nil
	}
}

// RemoveConstraint handles DELETE /tasks/{id}/constraints/{cid}.
func RemoveConstraint(deps Deps) func(context.Context, *RemoveConstraintInput) (*RemoveConstraintOutput, error) {
	return func(ctx context.Context, in *RemoveConstraintInput) (*RemoveConstraintOutput, error) {
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
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        "task.constraint.removed",
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":       task.PublicID.String(),
				"constraintId": cid.String(),
			},
		})
		out := &RemoveConstraintOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
