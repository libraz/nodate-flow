// Package taskactors owns the one way a user is attached to or removed
// from a task. The rows and the timeline event are written here, on the
// caller's transaction; the audit row is the caller's, written with
// RecordTxAudit after the commit.
package taskactors

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/taskworkflow"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

// ErrAlreadyActor is returned by [Add] when the user already holds the role.
var ErrAlreadyActor = errors.New("taskactors: user already holds the role on the task")

// ErrNotActor is returned by [Remove] when the task or the actor row does
// not exist in the workspace.
var ErrNotActor = errors.New("taskactors: no such actor on the task")

// Args describes one attach or detach. Internal ids drive the writes; the
// public ids only build the event payload.
type Args struct {
	// Actor is who makes the change; Actor.WorkspaceID scopes every query.
	Actor mutationlog.Actor

	TaskID       uint32
	TaskPublicID string

	// Add: the user to attach, the role, and the public id of the new row.
	UserID       uint32
	UserPublicID string
	Role         generated.TaskActorsRole
	PublicID     types.PublicID

	// Remove: the public id of the actor row, and the recorder used to
	// cancel the removed holder's pending handoff in the same transaction.
	ActorPublicID types.PublicID
	Mutations     *mutationlog.Recorder
}

// Add attaches the user and appends task.actor.added on tx. tx must come
// from dbretry.InTx.
func Add(ctx context.Context, tx *dbretry.Tx, args Args) error {
	if args.UserID > math.MaxInt32 {
		return errors.New("taskactors: user id out of range")
	}
	q := generated.New(tx)
	_, err := q.FindActiveTaskActor(ctx, generated.FindActiveTaskActorParams{
		WorkspaceID: args.Actor.WorkspaceID,
		TaskID:      args.TaskID,
		UserID:      sql.NullInt32{Int32: int32(args.UserID), Valid: true}, //#nosec G115 -- bounded by the MaxInt32 check above
		Role:        args.Role,
	})
	if err == nil {
		return ErrAlreadyActor
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := q.AddActor(ctx, generated.AddActorParams{
		PublicID:    args.PublicID,
		WorkspaceID: args.Actor.WorkspaceID,
		TaskID:      args.TaskID,
		UserID:      sql.NullInt32{Int32: int32(args.UserID), Valid: true}, //#nosec G115 -- bounded by the MaxInt32 check above
		Role:        args.Role,
	}); err != nil {
		return err
	}
	taskID := int64(args.TaskID)
	return eventbus.Append(ctx, tx, eventbus.Event{
		Type:         eventbus.TaskActorAdded,
		WorkspaceID:  args.Actor.WorkspaceID,
		ActorUserID:  userPtr(args.Actor),
		ActorAgentID: agentPtr(args.Actor),
		TaskID:       &taskID,
		Payload: map[string]any{
			"taskId":  args.TaskPublicID,
			"actorId": args.PublicID.String(),
			"userId":  args.UserPublicID,
			"role":    string(args.Role),
		},
	})
}

// Remove locks the task, detaches the actor, cancels a removed user
// holder's pending handoff, and appends task.actor.removed on tx.
func Remove(ctx context.Context, tx *dbretry.Tx, args Args) error {
	q := generated.New(tx)
	ws := args.Actor.WorkspaceID
	// Task row first, like every other task mutation, so removal
	// serializes with review and handoff decisions.
	if _, err := q.LockTaskForTransition(ctx, generated.LockTaskForTransitionParams{ID: args.TaskID, WorkspaceID: ws}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotActor
		}
		return err
	}
	holder, err := q.FindTaskActorForRemoval(ctx, generated.FindTaskActorForRemovalParams{
		WorkspaceID: ws,
		TaskID:      args.TaskID,
		PublicID:    args.ActorPublicID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotActor
	}
	if err != nil {
		return err
	}
	rows, err := q.RemoveActor(ctx, generated.RemoveActorParams{
		WorkspaceID: ws,
		TaskID:      args.TaskID,
		PublicID:    args.ActorPublicID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotActor
	}
	if holder.Kind == generated.TaskActorsKindUser {
		if err := taskworkflow.CancelPendingHandoffFromHolderTx(ctx, tx, args.Mutations, args.Actor.UserID, ws, args.TaskID,
			args.TaskPublicID, holder.Role, holder.UserID); err != nil {
			return err
		}
	}
	taskID := int64(args.TaskID)
	return eventbus.Append(ctx, tx, eventbus.Event{
		Type:         eventbus.TaskActorRemoved,
		WorkspaceID:  ws,
		ActorUserID:  userPtr(args.Actor),
		ActorAgentID: agentPtr(args.Actor),
		TaskID:       &taskID,
		Payload: map[string]any{
			"taskId":  args.TaskPublicID,
			"actorId": args.ActorPublicID.String(),
		},
	})
}

func userPtr(a mutationlog.Actor) *int64 {
	if a.UserID == 0 || a.AgentID != 0 {
		return nil
	}
	v := int64(a.UserID)
	return &v
}

func agentPtr(a mutationlog.Actor) *int64 {
	if a.AgentID == 0 {
		return nil
	}
	v := int64(a.AgentID)
	return &v
}
