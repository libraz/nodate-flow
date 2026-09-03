// Package sqlviews exercises view-level guards that protect against
// leaking child rows when their parent task has been soft-disabled
// (`tasks.enabled = FALSE`). Every view that surfaces
// rows from task_actors / task_labels / task_constraints /
// task_dependencies must propagate `enabled = TRUE` from the parent
// task so a single missed WHERE clause cannot leak archived data.
package sqlviews

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var (
	testSrv *helpers.TestServer
	testDB  *sql.DB
)

func TestMain(m *testing.M) {
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		os.Exit(m.Run())
	}
	inst, err := helpers.EnsureShared()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sqlviews: start shared mysql:", err)
		os.Exit(1)
	}
	srv, cleanup, err := helpers.NewTestServer(inst.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sqlviews: start test server:", err)
		os.Exit(1)
	}
	testSrv = srv
	testDB = inst.DB
	helpers.RegisterCleanupDB(inst.DB)
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func skipIfNoIntegration(t *testing.T) {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
}

// fixture bundles the IDs the test needs to assert on the views.
type fixture struct {
	wsID         uint32
	wsPublicID   string
	projectID    uint32
	userID       uint32
	parentTaskID uint32
	otherTaskID  uint32
	parentTaskPb string
	otherTaskPb  string
}

// seedFixture creates a tenant via the public API, then uses raw SQL to
// resolve internal IDs and insert a second task plus one row in each of
// task_actors / task_labels / task_constraints / task_dependencies.
// Direct inserts are used because the goal is to probe the view layer
// in isolation, not the higher-level handler stack.
func seedFixture(t *testing.T) *fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	tt := helpers.CreateTestTenant(t, testSrv.BaseURL)
	t.Cleanup(func() {
		helpers.CleanupTenant(t, tt)
		helpers.PurgeWorkspace(t, testDB, tt.WorkspacePublicID)
	})

	var f fixture
	f.wsPublicID = tt.WorkspacePublicID

	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0)`,
		tt.WorkspacePublicID).Scan(&f.wsID))
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE public_id = UUID_TO_BIN(?, 0)`,
		tt.ProjectPublicID).Scan(&f.projectID))
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM users WHERE public_id = UUID_TO_BIN(?, 0)`,
		tt.UserPublicID).Scan(&f.userID))

	parentPub := uuid.Must(uuid.NewV7())
	otherPub := uuid.Must(uuid.NewV7())
	f.parentTaskPb = parentPub.String()
	f.otherTaskPb = otherPub.String()

	parentBytes := parentPub[:]
	otherBytes := otherPub[:]

	res, err := testDB.ExecContext(ctx, `
		INSERT INTO tasks (public_id, workspace_id, project_id, task_number, created_by_user_id, title)
		VALUES (?, ?, ?, 1, ?, 'parent task')`,
		parentBytes, f.wsID, f.projectID, f.userID)
	require.NoError(t, err)
	id64, err := res.LastInsertId()
	require.NoError(t, err)
	f.parentTaskID = uint32(id64) //nolint:gosec // test-scoped LastInsertId fits uint32

	res, err = testDB.ExecContext(ctx, `
		INSERT INTO tasks (public_id, workspace_id, project_id, task_number, created_by_user_id, title)
		VALUES (?, ?, ?, 2, ?, 'other task')`,
		otherBytes, f.wsID, f.projectID, f.userID)
	require.NoError(t, err)
	id64, err = res.LastInsertId()
	require.NoError(t, err)
	f.otherTaskID = uint32(id64) //nolint:gosec // test-scoped LastInsertId fits uint32

	// task_actors: assignee user.
	actorPub := uuid.Must(uuid.NewV7())
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO task_actors (public_id, workspace_id, task_id, user_id, kind, role)
		VALUES (?, ?, ?, ?, 'user', 'assignee')`,
		actorPub[:], f.wsID, f.parentTaskID, f.userID)
	require.NoError(t, err)

	// labels + task_labels.
	labelPub := uuid.Must(uuid.NewV7())
	labelRes, err := testDB.ExecContext(ctx, `
		INSERT INTO labels (public_id, workspace_id, name, color)
		VALUES (?, ?, 'lbl-m7', '#abcdef')`,
		labelPub[:], f.wsID)
	require.NoError(t, err)
	labelID64, err := labelRes.LastInsertId()
	require.NoError(t, err)
	labelID := uint32(labelID64) //nolint:gosec // test-scoped LastInsertId fits uint32
	tlPub := uuid.Must(uuid.NewV7())
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO task_labels (public_id, workspace_id, task_id, label_id)
		VALUES (?, ?, ?, ?)`,
		tlPub[:], f.wsID, f.parentTaskID, labelID)
	require.NoError(t, err)

	// task_constraints: minimal deadline expression.
	tcPub := uuid.Must(uuid.NewV7())
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO task_constraints (public_id, workspace_id, task_id, kind, expression)
		VALUES (?, ?, ?, 'deadline', '{}')`,
		tcPub[:], f.wsID, f.parentTaskID)
	require.NoError(t, err)

	// task_dependencies: parent -> other.
	tdPub := uuid.Must(uuid.NewV7())
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO task_dependencies (public_id, workspace_id, from_task_id, to_task_id, kind)
		VALUES (?, ?, ?, ?, 'blocks')`,
		tdPub[:], f.wsID, f.parentTaskID, f.otherTaskID)
	require.NoError(t, err)

	return &f
}

// setTaskEnabled flips tasks.enabled for the given internal task id.
func setTaskEnabled(t *testing.T, taskID uint32, enabled bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := testDB.ExecContext(ctx,
		`UPDATE tasks SET enabled = ? WHERE id = ?`, enabled, taskID)
	require.NoError(t, err)
}

// scanInt runs a single-row, single-int query. Returns 0 for sql.ErrNoRows.
func scanInt(t *testing.T, query string, args ...any) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n sql.NullInt64
	err := testDB.QueryRowContext(ctx, query, args...).Scan(&n)
	if err == sql.ErrNoRows {
		return 0
	}
	require.NoError(t, err)
	if !n.Valid {
		return 0
	}
	return int(n.Int64)
}

// TestParentDisabledHidesChildrenFromViews verifies that disabling the
// parent task removes its actor / label / constraint / dependency rows
// from every view that surfaces them. v_task_detail is the canonical
// reference; v_task_list_all and v_my_tasks are the
// defense-in-depth additions.
func TestParentDisabledHidesChildrenFromViews(t *testing.T) {
	skipIfNoIntegration(t)
	t.Parallel()

	f := seedFixture(t)

	// Sanity: with the parent enabled the child rows must surface.
	require.Equal(t, 1, scanInt(t,
		`SELECT actor_count FROM v_task_detail
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, f.parentTaskPb))
	require.Equal(t, 1, scanInt(t,
		`SELECT label_count FROM v_task_detail
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, f.parentTaskPb))
	require.Equal(t, 1, scanInt(t,
		`SELECT constraint_count FROM v_task_detail
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, f.parentTaskPb))
	require.Equal(t, 1, scanInt(t,
		`SELECT dependency_count FROM v_task_detail
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, f.parentTaskPb))
	require.Equal(t, 1, scanInt(t,
		`SELECT assignee_count FROM v_task_list_all
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, f.parentTaskPb))
	require.Equal(t, 1, scanInt(t,
		`SELECT COUNT(*) FROM v_task_list_all
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) AND label_ids IS NOT NULL`,
		f.wsID, f.parentTaskPb))
	require.Equal(t, 1, scanInt(t,
		`SELECT COUNT(*) FROM v_my_tasks
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, f.parentTaskPb))

	// Disable the parent task; every dependent row must vanish.
	setTaskEnabled(t, f.parentTaskID, false)

	require.Equal(t, 0, scanInt(t,
		`SELECT COUNT(*) FROM v_task_detail
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, f.parentTaskPb),
		"v_task_detail must drop the disabled task row entirely")

	require.Equal(t, 0, scanInt(t,
		`SELECT COUNT(*) FROM v_task_list_all
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, f.parentTaskPb),
		"v_task_list_all must drop the disabled task row entirely")

	require.Equal(t, 0, scanInt(t,
		`SELECT COUNT(*) FROM v_my_tasks
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, f.parentTaskPb),
		"v_my_tasks must drop the actor row whose parent task is disabled")
}

// TestDependencyToTaskDisabledHidesEdge covers the SECOND parent of
// task_dependencies: with from_task enabled but to_task disabled, the
// dependency edge must not be counted in v_task_detail.dependency_count.
func TestDependencyToTaskDisabledHidesEdge(t *testing.T) {
	skipIfNoIntegration(t)
	t.Parallel()

	f := seedFixture(t)

	require.Equal(t, 1, scanInt(t,
		`SELECT dependency_count FROM v_task_detail
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, f.parentTaskPb))

	setTaskEnabled(t, f.otherTaskID, false)

	require.Equal(t, 0, scanInt(t,
		`SELECT dependency_count FROM v_task_detail
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0)`,
		f.wsID, f.parentTaskPb),
		"dependency_count must drop edges whose to_task is disabled")
}
