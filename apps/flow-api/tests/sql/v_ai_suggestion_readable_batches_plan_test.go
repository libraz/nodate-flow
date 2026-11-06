package sqlviews

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// TestReadableSuggestionBatchesPlanStaysInTheWorkspace pins the plan of a
// batch lookup: every table is reached through the caller's predicates, so
// no step is a derived table built from every batch in the instance.
func TestReadableSuggestionBatchesPlanStaysInTheWorkspace(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Real rows: against empty tables the optimizer short-circuits the plan.
	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID) })
	wsID, ok := helpers.WorkspaceInternalID(t, testDB, tt.WorkspacePublicID)
	require.True(t, ok)
	var userID uint32
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0)`, tt.UserPublicID).Scan(&userID))
	batchPublicID := types.New()
	for _, pid := range []types.PublicID{batchPublicID, types.New()} {
		_, err := testDB.ExecContext(ctx, `INSERT INTO ai_suggestion_batches
  (public_id, workspace_id, user_id, input_count, provenance_valid) VALUES (?, ?, ?, 0, TRUE)`,
			pid, wsID, userID)
		require.NoError(t, err)
	}

	const lookup = `SELECT b.id
FROM v_ai_suggestion_readable_batches b
WHERE b.workspace_id = ? AND b.user_id = ? AND b.public_id = ?
LIMIT 1`
	args := []any{wsID, userID, batchPublicID}

	var tree string
	require.NoError(t, testDB.QueryRowContext(ctx, "EXPLAIN FORMAT=TREE "+lookup, args...).Scan(&tree))
	t.Logf("plan tree:\n%s", tree)

	rows, err := testDB.QueryContext(ctx, "EXPLAIN FORMAT=TRADITIONAL "+lookup, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	require.NoError(t, err)
	selectTypeIdx, tableIdx := -1, -1
	for i, c := range cols {
		switch c {
		case "select_type":
			selectTypeIdx = i
		case "table":
			tableIdx = i
		}
	}
	require.NotEqual(t, -1, selectTypeIdx, "EXPLAIN has a select_type column")
	require.NotEqual(t, -1, tableIdx, "EXPLAIN has a table column")

	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		require.NoError(t, rows.Scan(ptrs...))
		t.Logf("plan: %s %s", vals[selectTypeIdx].String, vals[tableIdx].String)
		require.NotEqualf(t, "DERIVED", vals[selectTypeIdx].String,
			"plan materializes %s before the workspace predicate applies", vals[tableIdx].String)
	}
	require.NoError(t, rows.Err())
}
