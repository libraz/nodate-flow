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

// knownTransitions enumerates every transition name accepted by the API.
// The value is irrelevant; membership is what matters.
var knownTransitions = map[string]struct{}{
	"start":    {},
	"block":    {},
	"unblock":  {},
	"submit":   {},
	"complete": {},
	"reopen":   {},
	"cancel":   {},
}

// nextState applies the v1 state machine described in ADR 0001. It returns
// the next derived_state and true on success, or an empty string and false
// when the (current, transition) pair is illegal.
func nextState(current generated.TasksDerivedState, transition string) (generated.TasksDerivedState, bool) {
	switch current {
	case generated.TasksDerivedStateOpen:
		switch transition {
		case "start":
			return generated.TasksDerivedStateWaiting, true
		case "cancel":
			return generated.TasksDerivedStateCancelled, true
		case "complete":
			return generated.TasksDerivedStateDone, true
		}
	case generated.TasksDerivedStateWaiting:
		switch transition {
		case "submit":
			return generated.TasksDerivedStateReview, true
		case "block":
			return generated.TasksDerivedStateOpen, true
		case "cancel":
			return generated.TasksDerivedStateCancelled, true
		}
	case generated.TasksDerivedStateReview:
		switch transition {
		case "complete":
			return generated.TasksDerivedStateDone, true
		case "reopen":
			return generated.TasksDerivedStateWaiting, true
		case "cancel":
			return generated.TasksDerivedStateCancelled, true
		}
	case generated.TasksDerivedStateDone:
		if transition == "reopen" {
			return generated.TasksDerivedStateWaiting, true
		}
	case generated.TasksDerivedStateCancelled:
		if transition == "reopen" {
			return generated.TasksDerivedStateOpen, true
		}
	}
	return "", false
}

// Transition handles POST /tasks/{id}/transitions. It applies a single
// state machine step, persists the new derived_state, and appends a
// `task.transition.<name>` event in the same transaction.
func Transition(deps Deps) func(context.Context, *TransitionTaskInput) (*TransitionTaskOutput, error) {
	return func(ctx context.Context, in *TransitionTaskInput) (*TransitionTaskOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		if _, ok := knownTransitions[in.Body.Transition]; !ok {
			return nil, httpErr(apierrors.WsTaskTransitionUnknown)
		}

		current, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.WsTaskNotFound)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		nextDerived, ok := nextState(current.DerivedState, in.Body.Transition)
		if !ok {
			return nil, httpErr(apierrors.WsTaskTransitionRejected)
		}

		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		defer func() { _ = tx.Rollback() }()

		qtx := generated.New(tx)
		if err := qtx.TransitionTaskState(ctx, generated.TransitionTaskStateParams{
			DerivedState: nextDerived,
			Column2:      string(nextDerived),
			WorkspaceID:  ws.ID,
			PublicID:     types.FromUUID(task.PublicID),
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		taskInternal := int64(task.ID)
		if err := eventbus.Append(ctx, tx, eventbus.Event{
			Type:        eventbus.TaskTransition(in.Body.Transition),
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			TaskID:      &taskInternal,
			Payload: map[string]any{
				"taskId":     task.PublicID.String(),
				"transition": in.Body.Transition,
				"fromState":  string(current.DerivedState),
				"toState":    string(nextDerived),
				"reason":     in.Body.Reason,
				"occurredAt": in.Body.OccurredAt,
			},
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := tx.Commit(); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		autoEvaluateConstraints(ctx, deps, ws.ID, task.ID)
		row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    types.FromUUID(task.PublicID),
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &TransitionTaskOutput{Body: rowToTaskFromFind(row)}, nil
	}
}
