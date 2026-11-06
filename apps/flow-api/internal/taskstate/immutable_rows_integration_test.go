package taskstate_test

import (
	"context"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// seedHandoff adds a pending handoff so every immutable review table holds
// a row for the fixture task.
func seedHandoff(t *testing.T, f reviewedWorkFixture) {
	t.Helper()
	_, err := workChangeDB(t).ExecContext(context.Background(), `INSERT INTO task_handoffs (
  public_id, workspace_id, task_id, role, from_user_id, to_user_id, requested_by_user_id
) VALUES (?, ?, ?, 'reviewer', ?, ?, ?)`, types.New(), f.workspaceID, f.taskID, f.userID, f.userID, f.userID)
	require.NoError(t, err, "seed handoff")
}

func countImmutableRows(t *testing.T, f reviewedWorkFixture) map[string]int {
	t.Helper()
	db := workChangeDB(t)
	counts := map[string]int{}
	for _, table := range []string{"task_handoffs", "task_submissions", "task_review_decisions"} {
		var n int
		require.NoError(t, db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM "+table+" WHERE workspace_id = ?", f.workspaceID).Scan(&n))
		counts[table] = n
	}
	return counts
}

func TestReviewHistoryRowsRefuseExplicitDelete(t *testing.T) {
	db := workChangeDB(t)
	f := seedReviewedWork(t, db, "review_required")
	seedHandoff(t, f)

	for _, table := range []string{"task_review_decisions", "task_submissions", "task_handoffs"} {
		_, err := db.ExecContext(context.Background(),
			"DELETE FROM "+table+" WHERE workspace_id = ?", f.workspaceID) //#nosec G202 -- table comes from the literal list above
		var myErr *mysql.MySQLError
		require.ErrorAsf(t, err, &myErr, "DELETE FROM %s must be refused", table)
		require.Equalf(t, [5]byte{'4', '5', '0', '0', '0'}, myErr.SQLState, "DELETE FROM %s: %v", table, err)
	}
	for table, n := range countImmutableRows(t, f) {
		require.Equalf(t, 1, n, "%s rows survive a refused delete", table)
	}
}

func TestReviewHistoryRowsCascadeWithTask(t *testing.T) {
	db := workChangeDB(t)
	f := seedReviewedWork(t, db, "review_required")
	seedHandoff(t, f)

	_, err := db.ExecContext(context.Background(),
		`DELETE FROM tasks WHERE workspace_id = ? AND id = ?`, f.workspaceID, f.taskID)
	require.NoError(t, err, "deleting the task cascades through the immutable tables")
	for table, n := range countImmutableRows(t, f) {
		require.Zerof(t, n, "%s rows follow their task", table)
	}
}

func TestReviewHistoryRowsCascadeWithWorkspace(t *testing.T) {
	db := workChangeDB(t)
	f := seedReviewedWork(t, db, "review_required")
	seedHandoff(t, f)

	_, err := db.ExecContext(context.Background(), `DELETE FROM workspaces WHERE id = ?`, f.workspaceID)
	require.NoError(t, err, "deleting the workspace cascades through the immutable tables")
	for table, n := range countImmutableRows(t, f) {
		require.Zerof(t, n, "%s rows follow their workspace", table)
	}
}

func TestPurgeWorkspaceClearsReviewHistory(t *testing.T) {
	db := workChangeDB(t)
	f := seedReviewedWork(t, db, "review_required")
	seedHandoff(t, f)

	helpers.PurgeWorkspace(t, db, f.workspacePublicID.String())
	for table, n := range countImmutableRows(t, f) {
		require.Zerof(t, n, "%s rows survive the purge", table)
	}
	_, stillThere := helpers.WorkspaceInternalID(t, db, f.workspacePublicID.String())
	require.False(t, stillThere, "workspace row survives the purge")
}
