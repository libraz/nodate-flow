package mcp_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestMCPCreateTaskAttributesTheCallerAndAssignsNobody pins both halves of
// what create_task writes about who a task belongs to.
//
// It is a contract, not an implementation detail: the two columns decide
// what somebody opening the task later reads. A creator naming the person
// whose token made the call gives them a colleague to ask what the task
// is for; the alternative — an unattributed row — reads as something the
// system produced, with nobody to ask. And an empty actor list is the
// honest answer to "who is doing this", because a tool call is a request
// for the work to exist and not an agreement by anyone to carry it out:
// filling the assignee in would put the caller's name on a queue they
// never accepted, and take the task out of everyone else's view of
// unassigned work.
//
// Both halves are asserted because either one can drift on its own.
// Switching to an unattributed create empties the creator and leaves the
// actor list as it is; self-assigning fills the actor list and leaves the
// creator as it is.
func TestMCPCreateTaskAttributesTheCallerAndAssignsNobody(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedMCPTrailFixture(t, db)

	deps := mcp.Deps{DB: db, Queries: generated.New(db)}
	ctx := context.Background()
	caller := mcp.NewTestSession(fx.userID, fx.wsID, []string{"write:workspace"})

	out, err := mcp.RunCreateTask(ctx, deps, caller, mcpTrailArgs(t, map[string]any{
		"projectId": fx.projectPub.String(),
		"title":     "Draft the migration plan",
	}))
	require.NoError(t, err)

	res, ok := out.(map[string]any)
	require.True(t, ok)
	taskID, ok := res["id"].(string)
	require.True(t, ok)
	pub := uuid.MustParse(taskID)

	var internalID uint32
	var createdBy, updatedBy sql.NullInt64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id, created_by_user_id, updated_by_user_id FROM tasks WHERE public_id = ?`,
		pub[:]).Scan(&internalID, &createdBy, &updatedBy))

	require.True(t, createdBy.Valid,
		"a task created through an agent is still somebody's; an unattributed row leaves the reader nobody to ask")
	require.Equal(t, int64(fx.userID), createdBy.Int64,
		"the creator has to be the user whose token made the call")
	require.True(t, updatedBy.Valid)
	require.Equal(t, int64(fx.userID), updatedBy.Int64,
		"the last writer is the same person, for the same reason")

	var actors int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_actors WHERE task_id = ?`, internalID).Scan(&actors))
	require.Equal(t, 0, actors,
		"asking for a task to exist is not agreeing to do it, so nobody is attached to it")

	// Named separately from the count above: a future change that attaches
	// a watcher is a different decision from one that attaches an
	// assignee, and only the second puts work on somebody's queue.
	var assignees int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_actors WHERE task_id = ? AND role = 'assignee'`,
		internalID).Scan(&assignees))
	require.Equal(t, 0, assignees,
		"the task has to arrive unassigned so triage can still see it as unclaimed work")
}
