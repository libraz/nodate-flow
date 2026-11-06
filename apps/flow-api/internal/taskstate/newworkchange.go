package taskstate

import (
	"context"
	"database/sql"
	stderrors "errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// PrepareWorkChangeTx reopens completed work when a caller has already
// established that the effective task content is changing. The caller owns
// that comparison and the content write; this helper owns only the canonical
// state transition, within the caller's transaction.
//
// The task row is current-read and locked before its workflow settings are
// inspected. Only review-required work in Done is reopened. Manual tasks and
// tasks in every other state keep their current state.
//
// Content writers are not yet routed through this helper.
func PrepareWorkChangeTx(ctx context.Context, tx *dbretry.Tx, p ApplyParams) (*apierrors.Spec, error) {
	if err := validateActor(p); err != nil {
		return apierrors.InternalUnexpected, err
	}

	qtx := generated.New(tx)
	settings, err := qtx.GetTaskWorkflowSettingsForUpdate(ctx, generated.GetTaskWorkflowSettingsForUpdateParams{
		WorkspaceID: p.WorkspaceID,
		ID:          p.TaskID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return apierrors.WsTaskNotFound, nil
		}
		return apierrors.InternalUnexpected, err
	}
	if settings.DerivedState != generated.TasksDerivedStateDone ||
		settings.CompletionPolicy != generated.TasksCompletionPolicyReviewRequired {
		return nil, nil
	}

	p.Transition = TransitionReopen
	_, spec, err := ApplyTransitionTx(ctx, tx, p)
	return spec, err
}
