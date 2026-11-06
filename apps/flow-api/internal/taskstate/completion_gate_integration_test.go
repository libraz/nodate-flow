package taskstate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
)

var errRollbackProbe = errors.New("roll back the completion probe")

// TestCompletionGateReadsCurrentPolicyAfterEarlierSnapshot holds the gate to
// the locked row even when the caller's transaction already took a snapshot
// before a stricter policy and a new work revision were committed.
func TestCompletionGateReadsCurrentPolicyAfterEarlierSnapshot(t *testing.T) {
	db := workChangeDB(t)
	f := seedReviewedWork(t, db, "manual")
	ctx := context.Background()
	for _, transition := range []string{taskstate.TransitionReopen, taskstate.TransitionSubmit} {
		require.NoError(t, dbretry.InTx(ctx, db, "taskstate_test.setup", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			_, spec, err := taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
				WorkspaceID: f.workspaceID,
				TaskID:      f.taskID,
				PublicID:    f.taskPublicID,
				Transition:  transition,
				Via:         "integration_test",
			})
			require.Nil(t, spec)
			return err
		}))
	}

	err := dbretry.InTx(ctx, db, "taskstate_test.complete_after_snapshot", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		var policy string
		require.NoError(t, tx.RawTx().QueryRowContext(ctx,
			`SELECT completion_policy FROM tasks WHERE id = ?`, f.taskID).Scan(&policy))
		require.Equal(t, "manual", policy)

		_, err := db.ExecContext(ctx,
			`UPDATE tasks SET completion_policy = 'review_required', title = 'Revised after approval' WHERE id = ?`, f.taskID)
		require.NoError(t, err)

		_, spec, err := taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
			WorkspaceID: f.workspaceID,
			TaskID:      f.taskID,
			PublicID:    f.taskPublicID,
			Transition:  taskstate.TransitionComplete,
			Via:         "integration_test",
		})
		require.NoError(t, err)
		require.Equal(t, apierrors.WsTaskTransitionRejected, spec,
			"the gate must see the committed review_required policy, not the transaction's earlier snapshot")
		return errRollbackProbe
	})
	require.ErrorIs(t, err, errRollbackProbe)
}
