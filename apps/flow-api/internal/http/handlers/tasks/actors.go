package tasks

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
)

// AddActor handles POST /tasks/{id}/actors.
func AddActor(deps Deps) func(context.Context, *AddTaskActorInput) (*AddTaskActorOutput, error) {
	return func(ctx context.Context, in *AddTaskActorInput) (*AddTaskActorOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		userPub, err := types.Parse(in.Body.UserID)
		if err != nil {
			return nil, httpErr(apierrors.WsMemberNotFound)
		}
		const q = `SELECT id FROM users WHERE public_id = ? AND enabled = TRUE LIMIT 1`
		var uid uint32
		if err := deps.DB.QueryRowContext(ctx, q, userPub).Scan(&uid); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsMemberNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		pub := types.New()
		if _, err := deps.Queries.AddActor(ctx, generated.AddActorParams{
			PublicID:    pub,
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
			UserID:      sql.NullInt32{Int32: int32(uid), Valid: true},
			Role:        generated.TaskActorsRole(in.Body.Role),
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskActorAdded,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":  task.PublicID.String(),
				"actorId": pub.String(),
				"userId":  userPub.String(),
				"role":    in.Body.Role,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.AddActor"),
				slog.String("event_type", string(eventbus.TaskActorAdded)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("actor_public_id", pub.String()),
			)
		}
		if aID, aOk := middleware.ActorFromContext(ctx); aOk {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.actor.add",
				ActorID:      aID,
				WorkspaceID:  ws.ID,
				ResourceType: "task_actor",
				ResourceID:   pub.String(),
			})
		}
		return &AddTaskActorOutput{Body: TaskActor{
			ID:     pub.String(),
			UserID: userPub.String(),
			Role:   in.Body.Role,
		}}, nil
	}
}

// ListActors handles GET /tasks/{id}/actors.
func ListActors(deps Deps) func(context.Context, *ListTaskActorsInput) (*ListTaskActorsOutput, error) {
	return func(ctx context.Context, in *ListTaskActorsInput) (*ListTaskActorsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 100
		}
		rows, err := deps.Queries.ListActorsForTask(ctx, generated.ListActorsForTaskParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListTaskActorsOutput{}
		out.Body.Actors = []TaskActor{}
		for _, r := range rows {
			out.Body.Actors = append(out.Body.Actors, rowToActor(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// RemoveActor handles DELETE /tasks/{id}/actors/{actorId}.
func RemoveActor(deps Deps) func(context.Context, *RemoveTaskActorInput) (*RemoveTaskActorOutput, error) {
	return func(ctx context.Context, in *RemoveTaskActorInput) (*RemoveTaskActorOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		aid, err := types.Parse(in.ActorID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		if err := deps.Queries.RemoveActor(ctx, generated.RemoveActorParams{
			WorkspaceID: ws.ID,
			PublicID:    aid,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskActorRemoved,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":  task.PublicID.String(),
				"actorId": aid.String(),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "tasks.RemoveActor"),
				slog.String("event_type", string(eventbus.TaskActorRemoved)),
				logutil.LogEntity("workspace", ws.PublicID),
				logutil.LogEntity("task", task.PublicID),
				slog.String("actor_public_id", aid.String()),
			)
		}
		if aID, aOk := middleware.ActorFromContext(ctx); aOk {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "task.actor.remove",
				ActorID:      aID,
				WorkspaceID:  ws.ID,
				ResourceType: "task_actor",
				ResourceID:   aid.String(),
			})
		}
		out := &RemoveTaskActorOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
