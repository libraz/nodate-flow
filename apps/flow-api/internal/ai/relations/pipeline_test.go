// External test package: tests/helpers imports the router, which imports
// this package, so an internal test importing helpers would be a cycle.
package relations_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/relations"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// TestBurstProcessesEveryEventsOwnTask fires a burst of task.updated
// hooks for different tasks and requires each task to be processed once.
// Reading "the latest event of this type" instead made every hook in a
// burst process the same task and the rest none.
func TestBurstProcessesEveryEventsOwnTask(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	wsPub := uuid.Must(uuid.NewV7())
	res, err := db.ExecContext(ctx, `INSERT INTO workspaces (public_id, slug, name) VALUES (?, ?, ?)`,
		wsPub[:], "ws-rel-"+suffix, "Relations burst")
	require.NoError(t, err)
	wsRaw, err := res.LastInsertId()
	require.NoError(t, err)
	wsID := uint32(wsRaw) //#nosec G115 -- LastInsertId in test seed, fits uint32
	prjPub := uuid.Must(uuid.NewV7())
	res, err = db.ExecContext(ctx, `INSERT INTO projects (public_id, workspace_id, slug, name) VALUES (?, ?, ?, ?)`,
		prjPub[:], wsID, "prj-rel-"+suffix, "Relations burst")
	require.NoError(t, err)
	prjRaw, err := res.LastInsertId()
	require.NoError(t, err)

	const n = 5
	var want []uint32
	var events []uint64
	for i := range n {
		taskPub := uuid.Must(uuid.NewV7())
		res, err := db.ExecContext(ctx,
			`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title) VALUES (?, ?, ?, ?, 'burst task')`,
			taskPub[:], wsID, prjRaw, i+1)
		require.NoError(t, err)
		taskRaw, err := res.LastInsertId()
		require.NoError(t, err)
		want = append(want, uint32(taskRaw)) //#nosec G115 -- LastInsertId in test seed, fits uint32
		evPub := uuid.Must(uuid.NewV7())
		res, err = db.ExecContext(ctx,
			`INSERT INTO events (public_id, workspace_id, type, task_id, payload_json, occurred_at)
			 VALUES (?, ?, 'task.updated', ?, JSON_OBJECT(), NOW(3))`,
			evPub[:], wsID, taskRaw)
		require.NoError(t, err)
		evRaw, err := res.LastInsertId()
		require.NoError(t, err)
		events = append(events, uint64(evRaw)) //#nosec G115 -- AUTO_INCREMENT LastInsertId is non-negative
	}

	var mu sync.Mutex
	var got []uint32
	p := &relations.Pipeline{DB: db, Runner: bgloop.NewHookRunner("test.relations", 2, 0)}
	p.SetProcess(func(_ context.Context, _ uint32, taskID uint32) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, taskID)
	})
	hook := p.Hook()
	for _, id := range events {
		hook(ctx, wsID, "task.updated", id)
	}
	require.NoError(t, p.Shutdown(ctx))

	slices.Sort(got)
	require.Equal(t, want, got, "each event must process the task it names, exactly once")
}
