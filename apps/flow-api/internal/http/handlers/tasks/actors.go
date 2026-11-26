package tasks

import (
	"context"
	"errors"
	"math"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskactors"
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
		actor := mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.AddActor", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			return taskactors.Add(ctx, tx, taskactors.Args{
				Actor:        actor,
				TaskID:       task.ID,
				TaskPublicID: task.PublicID.String(),
				UserID:       uint32(uid), //#nosec G115 -- uid is bounded by the math.MaxInt32 check above
				UserPublicID: userPub.String(),
				Role:         role,
				PublicID:     pub,
			})
		})
		if errors.Is(txErr, taskactors.ErrAlreadyActor) {
			// Attaching a role the user already holds is a no-op that
			// answers with the existing row.
			existing, err := deps.Queries.ListActorsForTask(ctx, generated.ListActorsForTaskParams{
				WorkspaceID: ws.ID,
				PublicID:    types.FromUUID(task.PublicID),
				Limit:       math.MaxInt32,
			})
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			emails, err := handlerutil.NewEmailVisibility(ctx, deps.Queries, ws.Role, actorID)
			if err != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			for _, r := range existing {
				if r.UserPublicID == userPub && r.Role == role {
					return &AddTaskActorOutput{Body: rowToActor(r, emails)}, nil
				}
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		deps.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
			AuditAction:  "task.actor.add",
			ResourceType: "task_actor",
			ResourceID:   pub.String(),
			Payload: map[string]any{
				"taskId":  task.PublicID.String(),
				"actorId": pub.String(),
				"userId":  userPub.String(),
				"role":    in.Body.Role,
			},
			CallSite: "tasks.AddActor (taskactors.Add appended the event)",
		})
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
		actorID, _ := middleware.ActorFromContext(ctx)
		emails, err := handlerutil.NewEmailVisibility(ctx, deps.Queries, ws.Role, actorID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &ListTaskActorsOutput{}
		out.Body.Actors = []TaskActor{}
		for _, r := range rows {
			out.Body.Actors = append(out.Body.Actors, rowToActor(r, emails))
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
		// The locked lookup in the kit is the existence check: a well-formed
		// id for an actor on another task -- or none at all -- must answer
		// not found rather than append a task.actor.removed event for a
		// removal that never happened.
		actorID, _ := middleware.ActorFromContext(ctx)
		actor := mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}
		txErr := dbretry.InTx(ctx, deps.DB, "tasks.RemoveActor", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			return taskactors.Remove(ctx, tx, taskactors.Args{
				Actor:         actor,
				TaskID:        task.ID,
				TaskPublicID:  task.PublicID.String(),
				ActorPublicID: aid,
				Mutations:     deps.Mutations,
			})
		})
		if errors.Is(txErr, taskactors.ErrNotActor) {
			return nil, httpErr(apierrors.WsTaskActorNotFound)
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		deps.Mutations.RecordTxAudit(ctx, actor, mutationlog.Mutation{
			AuditAction:  "task.actor.remove",
			ResourceType: "task_actor",
			ResourceID:   aid.String(),
			Payload: map[string]any{
				"taskId":  task.PublicID.String(),
				"actorId": aid.String(),
			},
			CallSite: "tasks.RemoveActor (taskactors.Remove appended the event)",
		})
		out := &RemoveTaskActorOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
