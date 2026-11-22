package taskdeps_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskdeps"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// TestTaskDepsRemove covers the removal path against a real database:
// the edge is soft-deleted and a removal event is appended, a sibling
// task's path finds nothing, and a repeated removal reports ErrNotFound
// without appending a second event.
func TestTaskDepsRemove(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst, err := helpers.EnsureShared()
	require.NoError(t, err, "start shared MySQL")
	db := inst.DB

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	suffix = suffix[len(suffix)-12:]
	wsPub := types.New()
	exec := func(query string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err, "seed exec %q", query)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- auto-increment ids of fresh rows fit uint32.
	}
	wsID := exec(`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`,
		wsPub, "td-"+suffix, "Taskdeps "+suffix)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, db, wsPub.String()) })
	userID := exec(`INSERT INTO users (public_id, email, display_name, locale, timezone) VALUES (?, ?, ?, 'en', 'UTC')`,
		types.New(), "taskdeps+"+suffix+"@example.test", "Taskdeps "+suffix)
	projectID := exec(`INSERT INTO projects (public_id, workspace_id, name, slug) VALUES (?, ?, ?, ?)`,
		types.New(), wsID, "Taskdeps Project", "td-"+suffix)
	newTask := func(n int) (uint32, types.PublicID) {
		pub := types.New()
		id := exec(`INSERT INTO tasks (public_id, workspace_id, project_id, created_by_user_id, task_number, title, priority, visibility)
VALUES (?, ?, ?, ?, ?, 'Taskdeps fixture', 1, 'public')`, pub, wsID, projectID, userID, n)
		return id, pub
	}
	fromID, fromPub := newTask(1)
	toID, toPub := newTask(2)
	siblingID, siblingPub := newTask(3)

	actor := int64(userID)
	var depID types.PublicID
	require.NoError(t, dbretry.InTx(ctx, db, "test.TaskDepsAdd", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		id, e := taskdeps.Add(ctx, tx, taskdeps.Args{
			WorkspaceID:      wsID,
			FromTaskID:       fromID,
			ToTaskID:         toID,
			Kind:             generated.TaskDependenciesKind("blocks"),
			ActorUserID:      &actor,
			FromTaskPublicID: fromPub.String(),
			ToTaskPublicID:   toPub.String(),
			Via:              "api",
		})
		depID = id
		return e
	}))

	remove := func(from uint32, fromPublic types.PublicID) error {
		return dbretry.InTx(ctx, db, "test.TaskDepsRemove", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			return taskdeps.Remove(ctx, tx, taskdeps.RemoveArgs{
				WorkspaceID:      wsID,
				FromTaskID:       from,
				DependencyID:     depID,
				ActorUserID:      &actor,
				FromTaskPublicID: fromPublic.String(),
			})
		})
	}
	removedEvents := func() int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = 'task.dependency.removed'`, wsID).Scan(&n))
		return n
	}
	enabled := func() bool {
		var e bool
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT enabled FROM task_dependencies WHERE workspace_id = ? AND public_id = ?`, wsID, depID).Scan(&e))
		return e
	}

	// A sibling's path must not touch the edge.
	require.ErrorIs(t, remove(siblingID, siblingPub), taskdeps.ErrNotFound)
	require.True(t, enabled())
	require.Zero(t, removedEvents())

	// The owning path removes it and records one event.
	require.NoError(t, remove(fromID, fromPub))
	require.False(t, enabled())
	require.Equal(t, 1, removedEvents())

	// A repeat is a no-op that appends nothing.
	require.ErrorIs(t, remove(fromID, fromPub), taskdeps.ErrNotFound)
	require.Equal(t, 1, removedEvents())
}
