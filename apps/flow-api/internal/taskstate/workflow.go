package taskstate

import (
	"context"
	"database/sql"
	stderrors "errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// SubmissionParams identifies the authenticated human who is submitting the
// task. PrepareSubmissionTx records any state transitions through
// ApplyTransitionTx in the caller's transaction.
type SubmissionParams struct {
	WorkspaceID uint32
	TaskID      uint32
	PublicID    types.PublicID
	ActorUserID *int64
	Via         string
}

// PrepareSubmissionTx checks whether the current task state can enter or
// remain in review and applies the canonical transitions atomically with the
// caller's submission write.
//
// Open tasks record Start and Submit at submission time. Waiting tasks record
// Submit unless their latest canonical transition is Block. Review tasks may
// be resubmitted without another transition. Terminal tasks and blocked waiting
// tasks are rejected until an explicit valid state transition changes them.
func PrepareSubmissionTx(ctx context.Context, tx *dbretry.Tx, p SubmissionParams) (*apierrors.Spec, error) {
	qtx := generated.New(tx)
	locked, err := qtx.LockTaskForTransition(ctx, generated.LockTaskForTransitionParams{
		ID:          p.TaskID,
		WorkspaceID: p.WorkspaceID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return apierrors.WsTaskNotFound, nil
		}
		return apierrors.InternalUnexpected, err
	}

	state := locked.DerivedState
	if state == generated.TasksDerivedStateWaiting {
		latest, err := qtx.GetLatestTaskTransitionForSubmission(ctx, generated.GetLatestTaskTransitionForSubmissionParams{
			WorkspaceID: p.WorkspaceID,
			TaskID:      sql.NullInt32{Int32: int32(p.TaskID), Valid: true}, //#nosec G115 -- tasks.id is an INT UNSIGNED key within the application's supported range
		})
		if err != nil && !stderrors.Is(err, sql.ErrNoRows) {
			return apierrors.InternalUnexpected, err
		}
		if err == nil && latest == string(eventbus.TaskTransitionBlock) {
			return apierrors.WsTaskTransitionRejected, nil
		}
	}

	apply := func(transition string) (*apierrors.Spec, error) {
		_, spec, applyErr := ApplyTransitionTx(ctx, tx, ApplyParams{
			WorkspaceID: p.WorkspaceID,
			TaskID:      p.TaskID,
			PublicID:    p.PublicID,
			Transition:  transition,
			ActorUserID: p.ActorUserID,
			Via:         p.Via,
		})
		return spec, applyErr
	}

	switch state {
	case generated.TasksDerivedStateOpen:
		if spec, err := apply(TransitionStart); err != nil || spec != nil {
			return spec, err
		}
		return apply(TransitionSubmit)
	case generated.TasksDerivedStateWaiting:
		return apply(TransitionSubmit)
	case generated.TasksDerivedStateReview:
		return nil, nil
	default:
		return apierrors.WsTaskTransitionRejected, nil
	}
}
