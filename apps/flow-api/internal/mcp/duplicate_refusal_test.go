package mcp_test

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// requireDuplicateRefusal asserts a refused duplicate carries the exact
// code and status the REST route answers for the same collision.
//
// The status is asserted alongside the code because the class is what a
// caller keys its behaviour on: a 4xx is final and a 5xx invites another
// attempt. A duplicate is the caller's to resolve, so it can never be the
// second.
func requireDuplicateRefusal(t *testing.T, err error, want *apierrors.Spec) {
	t.Helper()
	require.Error(t, err, "a collision with a live row has to be reported")
	var ae *apierrors.APIError
	require.Truef(t, stderrors.As(err, &ae), "want *apierrors.APIError, got %T: %v", err, err)
	require.NotNil(t, ae.Spec)
	require.Equal(t, want.Code, ae.Spec.Code)
	require.Equal(t, want.Status, ae.Spec.Status)
	require.Lessf(t, ae.Spec.Status, 500,
		"%s tells an agent to retry a call that cannot succeed", ae.Spec.Code)
}

// TestMCPDuplicateKeyRefusalsAreNamed drives the tools whose insert can
// collide with a row that is already there, and pins what each one
// answers.
//
// The state the caller asked for already holds, so repeating the call
// cannot change the outcome. Reported as a tool-execution failure the
// refusal reads as a server fault — the one class an agent is supposed to
// retry — so the loop spun on a call that could never succeed and the
// caller never learned what was actually wrong. Each code below is the one
// the REST route answers for the same collision, so a client cannot tell
// the two transports apart by the refusal it gets.
func TestMCPDuplicateKeyRefusalsAreNamed(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB

	deps := mcp.Deps{DB: db, Queries: generated.New(db)}
	ctx := context.Background()

	// Each case takes its own workspace, so a name that has to collide
	// inside one case cannot collide with a parallel run of another.
	t.Run("add_task_label/label_already_on_the_task", func(t *testing.T) {
		fx := seedMCPWiringFixture(t, db)
		sess := mcp.NewTestSession(fx.userID, fx.wsID, []string{"write:workspace"})

		out, err := mcp.RunCreateLabel(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"name":  "Blocked",
			"color": "#ef4444",
		}))
		require.NoError(t, err)
		created, ok := out.(map[string]any)
		require.True(t, ok)
		labelID, ok := created["id"].(string)
		require.True(t, ok)

		attach := func() error {
			_, aerr := mcp.RunAddTaskLabel(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
				"taskId":  fx.taskPub.String(),
				"labelId": labelID,
			}))
			return aerr
		}

		// The control: without a first attach that succeeds, a refusal on
		// the second proves nothing about duplicates.
		require.NoError(t, attach())
		requireDuplicateRefusal(t, attach(), apierrors.WsLabelNameAlreadyTaken)

		// A refused attach must not have written a second junction row.
		var n int
		require.NoError(t, db.QueryRow(
			`SELECT COUNT(*) FROM task_labels WHERE task_id = ? AND enabled = TRUE`,
			fx.taskInternalID).Scan(&n))
		require.Equal(t, 1, n)
	})

	t.Run("create_timebox/name_already_in_the_workspace", func(t *testing.T) {
		fx := seedMCPWiringFixture(t, db)
		sess := mcp.NewTestSession(fx.userID, fx.wsID, []string{"write:workspace"})

		create := func() error {
			_, cerr := mcp.RunCreateTimebox(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
				"name":     "Sprint 1",
				"startsOn": "2026-01-05",
				"endsOn":   "2026-01-19",
			}))
			return cerr
		}

		require.NoError(t, create())
		requireDuplicateRefusal(t, create(), apierrors.TimeboxTimeboxNameTaken)

		var n int
		require.NoError(t, db.QueryRow(
			`SELECT COUNT(*) FROM timeboxes WHERE workspace_id = ? AND name = 'Sprint 1'`,
			fx.wsID).Scan(&n))
		require.Equal(t, 1, n, "a refused create must not have left a second timebox behind")
	})

	t.Run("add_task_to_timebox/task_already_in_the_timebox", func(t *testing.T) {
		fx := seedMCPWiringFixture(t, db)
		sess := mcp.NewTestSession(fx.userID, fx.wsID, []string{"write:workspace"})

		out, err := mcp.RunCreateTimebox(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"name":     "Sprint 2",
			"startsOn": "2026-02-02",
			"endsOn":   "2026-02-16",
		}))
		require.NoError(t, err)
		created, ok := out.(map[string]any)
		require.True(t, ok)
		timeboxID, ok := created["id"].(string)
		require.True(t, ok)

		add := func() error {
			_, aerr := mcp.RunAddTaskToTimebox(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
				"timeboxId": timeboxID,
				"taskId":    fx.taskPub.String(),
			}))
			return aerr
		}

		require.NoError(t, add())
		requireDuplicateRefusal(t, add(), apierrors.TimeboxTaskAlreadyAdded)

		var n int
		require.NoError(t, db.QueryRow(
			`SELECT COUNT(*) FROM timebox_tasks WHERE workspace_id = ? AND task_id = ? AND enabled = TRUE`,
			fx.wsID, fx.taskInternalID).Scan(&n))
		require.Equal(t, 1, n)
	})
}
