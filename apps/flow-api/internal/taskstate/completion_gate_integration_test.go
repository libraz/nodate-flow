package taskstate_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
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

// TestGateEvaluatesWithFreshFactsAfterEarlierSnapshot holds the gate's
// constraint evaluation to current facts: a required dependency.all_done
// constraint whose blocker was completed on another connection after the
// transition's transaction took its snapshot must let the completion pass.
func TestGateEvaluatesWithFreshFactsAfterEarlierSnapshot(t *testing.T) {
	db := workChangeDB(t)
	f := seedReviewedWork(t, db, "review_required")
	ctx := context.Background()
	for _, transition := range []string{taskstate.TransitionReopen, taskstate.TransitionSubmit} {
		require.NoError(t, applyInOwnTx(ctx, db, f.workspaceID, f.taskID, f.taskPublicID, transition))
	}
	blockerID, blockerPublicID := seedRequiredDependency(t, db, f)

	completeTarget := func(ctx context.Context, tx *dbretry.Tx) *apierrors.Spec {
		_, spec, err := taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
			WorkspaceID: f.workspaceID,
			TaskID:      f.taskID,
			PublicID:    f.taskPublicID,
			Transition:  taskstate.TransitionComplete,
			Via:         "integration_test",
		})
		require.NoError(t, err)
		return spec
	}

	err := dbretry.InTx(ctx, db, "taskstate_test.complete_before_blocker", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		require.Equal(t, apierrors.WsTaskTransitionRejected, completeTarget(ctx, tx),
			"an open blocker must fail the required constraint and reject the completion")
		return errRollbackProbe
	})
	require.ErrorIs(t, err, errRollbackProbe)

	err = dbretry.InTx(ctx, db, "taskstate_test.complete_after_snapshot", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		readBlockerState := func() string {
			var state string
			require.NoError(t, tx.RawTx().QueryRowContext(ctx,
				`SELECT derived_state FROM tasks WHERE id = ?`, blockerID).Scan(&state))
			return state
		}
		require.Equal(t, "open", readBlockerState())

		require.NoError(t, applyInOwnTx(ctx, db, f.workspaceID, blockerID, blockerPublicID, taskstate.TransitionComplete))
		require.Equal(t, "open", readBlockerState(),
			"the transaction's snapshot must predate the blocker's completion for this test to mean anything")

		require.Nil(t, completeTarget(ctx, tx),
			"the gate must evaluate the committed blocker state, not the transaction's earlier snapshot")

		var verdicts int
		require.NoError(t, tx.RawTx().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM events WHERE task_id = ? AND type = ?`,
			f.taskID, string(eventbus.TaskConstraintSatisfied)).Scan(&verdicts))
		require.Equal(t, 1, verdicts, "the changed verdict must be appended in the transition's transaction")
		return errRollbackProbe
	})
	require.ErrorIs(t, err, errRollbackProbe)
}

func applyInOwnTx(ctx context.Context, db *sql.DB, workspaceID, taskID uint32, publicID types.PublicID, transition string) error {
	return dbretry.InTx(ctx, db, "taskstate_test.setup", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		_, spec, err := taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
			WorkspaceID: workspaceID,
			TaskID:      taskID,
			PublicID:    publicID,
			Transition:  transition,
			Via:         "integration_test",
		})
		if err != nil {
			return err
		}
		if spec != nil {
			return fmt.Errorf("transition %s refused: %s", transition, spec.Code)
		}
		return nil
	})
}

// seedRequiredDependency gives the fixture task an open blocker and a
// dependency.all_done constraint on it, selected as a completion
// requirement. It returns the blocker's internal and public ids.
func seedRequiredDependency(t *testing.T, db *sql.DB, f reviewedWorkFixture) (uint32, types.PublicID) {
	t.Helper()
	ctx := context.Background()
	blockerPublicID := types.New()
	res, err := db.ExecContext(ctx, `INSERT INTO tasks (
  public_id, workspace_id, project_id, created_by_user_id, task_number, title, derived_state
) VALUES (?, ?, ?, ?, 2, 'Blocker', 'open')`, blockerPublicID, f.workspaceID, f.projectID, f.userID)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	blockerID := uint32(id) //#nosec G115 -- test fixture IDs are INT UNSIGNED

	_, err = db.ExecContext(ctx, `INSERT INTO task_dependencies (public_id, workspace_id, from_task_id, to_task_id, kind)
VALUES (?, ?, ?, ?, 'blocks')`, types.New(), f.workspaceID, f.taskID, blockerID)
	require.NoError(t, err)

	constraintID := types.New()
	_, err = db.ExecContext(ctx, `INSERT INTO task_constraints (public_id, workspace_id, task_id, kind, expression)
VALUES (?, ?, ?, 'dependency', ?)`, constraintID, f.workspaceID, f.taskID,
		fmt.Sprintf(`{"op":"dependency.all_done","taskIds":[%q]}`, blockerPublicID.String()))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE tasks SET required_completion_constraint_ids = JSON_ARRAY(?) WHERE id = ?`,
		constraintID.String(), f.taskID)
	require.NoError(t, err)
	return blockerID, blockerPublicID
}
