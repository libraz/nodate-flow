package tasks

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/taskworkflow"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
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
		// Resolve the target user scoped to this workspace so callers cannot
		// attach a user from another tenant as a task actor.
		uid, err := deps.Queries.FindWorkspaceMemberUserInternalIdByPublicId(ctx, generated.FindWorkspaceMemberUserInternalIdByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    userPub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsMemberNotFound, apierrors.InternalUnexpected))
		}
		role, perr := parseActorRole(in.Body.Role)
		if perr != nil {
			return nil, translateActorRoleError(perr)
		}
		if uid > math.MaxInt32 {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		pub := types.New()
		actorID, _ := middleware.ActorFromContext(ctx)
		taskInternal := int64(task.ID)
		if err := dbretry.InTx(ctx, deps.DB, "tasks.AddActor", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			if _, err := generated.New(tx).AddActor(ctx, generated.AddActorParams{
				PublicID:    pub,
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				UserID:      sql.NullInt32{Int32: int32(uid), Valid: true}, //#nosec G115 -- uid is bounded by the math.MaxInt32 check above
				Role:        role,
			}); err != nil {
				return err
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.TaskActorAdded,
				AuditAction:  "task.actor.add",
				ResourceType: "task_actor",
				ResourceID:   pub.String(),
				TaskID:       &taskInternal,
				Payload: map[string]any{
					"taskId":  task.PublicID.String(),
					"actorId": pub.String(),
					"userId":  userPub.String(),
					"role":    in.Body.Role,
				},
				CallSite: "tasks.AddActor",
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
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
		// The locked lookup is the existence check: a well-formed id for an
		// actor on another task -- or none at all -- must answer not found
		// rather than append a task.actor.removed event for a removal that
		// never happened.
		actorID, _ := middleware.ActorFromContext(ctx)
		taskInternal := int64(task.ID)
		var missing bool
		if err := dbretry.InTx(ctx, deps.DB, "tasks.RemoveActor", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			qtx := generated.New(tx)
			// Task row first, like every other task mutation, so removal
			// serializes with review and handoff decisions.
			if _, err := qtx.LockTaskForTransition(ctx, generated.LockTaskForTransitionParams{
				ID:          task.ID,
				WorkspaceID: ws.ID,
			}); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					missing = true
					return nil
				}
				return err
			}
			holder, err := qtx.FindTaskActorForRemoval(ctx, generated.FindTaskActorForRemovalParams{
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				PublicID:    aid,
			})
			if errors.Is(err, sql.ErrNoRows) {
				missing = true
				return nil
			}
			if err != nil {
				return err
			}
			rows, err := qtx.RemoveActor(ctx, generated.RemoveActorParams{
				WorkspaceID: ws.ID,
				TaskID:      task.ID,
				PublicID:    aid,
			})
			if err != nil {
				return err
			}
			if missing = rows == 0; missing {
				return nil
			}
			if holder.Kind == generated.TaskActorsKindUser {
				if err := taskworkflow.CancelPendingHandoffFromHolderTx(ctx, tx, deps.Mutations, actorID, ws.ID, task.ID,
					task.PublicID.String(), holder.Role, holder.UserID); err != nil {
					return err
				}
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.TaskActorRemoved,
				AuditAction:  "task.actor.remove",
				ResourceType: "task_actor",
				ResourceID:   aid.String(),
				TaskID:       &taskInternal,
				Payload: map[string]any{
					"taskId":  task.PublicID.String(),
					"actorId": aid.String(),
				},
				CallSite: "tasks.RemoveActor",
			})
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if missing {
			return nil, httpErr(apierrors.WsTaskActorNotFound)
		}
		out := &RemoveTaskActorOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
