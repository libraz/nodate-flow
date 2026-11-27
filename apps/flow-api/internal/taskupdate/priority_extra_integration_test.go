package taskupdate_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskupdate"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

type priorityFixture struct {
	t      *testing.T
	ctx    context.Context
	wsID   uint32
	userID uint32
	taskID uint32
	pub    types.PublicID
	apply  func(taskupdate.Patch, map[string]any) (taskupdate.Result, error)
	count  func(query string, args ...any) int
}

func newPriorityFixture(t *testing.T) *priorityFixture {
	testhelpers.SkipUnlessIntegration(t)
	inst, err := helpers.EnsureShared()
	require.NoError(t, err, "start shared MySQL")
	db := inst.DB
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	suffix = suffix[len(suffix)-12:]
	wsPub := types.New()
	seed := func(query string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err, "seed %q", query)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- auto-increment ids of fresh rows fit uint32.
	}
	f := &priorityFixture{t: t, ctx: ctx}
	f.wsID = seed(`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`,
		wsPub, "tp-"+suffix, "Taskupdate priority "+suffix)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, db, wsPub.String()) })
	f.userID = seed(`INSERT INTO users (public_id, email, display_name, locale, timezone) VALUES (?, ?, ?, 'en', 'UTC')`,
		types.New(), "taskupdate-prio+"+suffix+"@example.test", "Prio "+suffix)
	projectID := seed(`INSERT INTO projects (public_id, workspace_id, name, slug) VALUES (?, ?, ?, ?)`,
		types.New(), f.wsID, "Prio Project", "tp-"+suffix)
	f.pub = types.New()
	f.taskID = seed(`INSERT INTO tasks (public_id, workspace_id, project_id, created_by_user_id, task_number, title, priority, visibility)
VALUES (?, ?, ?, ?, 1, 'Original', 1, 'public')`, f.pub, f.wsID, projectID, f.userID)
	f.apply = func(patch taskupdate.Patch, extra map[string]any) (taskupdate.Result, error) {
		var (
			res      taskupdate.Result
			applyErr error
		)
		_ = dbretry.InTx(ctx, db, "test.TaskUpdatePriority", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			res, applyErr = taskupdate.Apply(ctx, tx, taskupdate.Args{
				WorkspaceID:  f.wsID,
				TaskID:       f.taskID,
				PublicID:     f.pub,
				Patch:        patch,
				Actor:        mutationlog.Actor{UserID: f.userID, WorkspaceID: f.wsID},
				ExtraPayload: extra,
			})
			return applyErr
		})
		return res, applyErr
	}
	f.count = func(query string, args ...any) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, query, args...).Scan(&n))
		return n
	}
	return f
}

func TestApply_ExpectedPriorityConflict(t *testing.T) {
	f := newPriorityFixture(t)
	i32 := func(v int32) *int32 { return &v }

	_, err := f.apply(taskupdate.Patch{Priority: i32(3), ExpectedPriority: i32(2)}, nil)
	require.ErrorIs(t, err, taskupdate.ErrPriorityConflict)
	require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM tasks WHERE id = ? AND priority = 1`, f.taskID))
	require.Zero(t, f.count(`SELECT COUNT(*) FROM events WHERE task_id = ? AND type = 'task.updated'`, f.taskID))

	_, err = f.apply(taskupdate.Patch{Priority: i32(3), ExpectedPriority: i32(1)}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, f.count(`SELECT COUNT(*) FROM tasks WHERE id = ? AND priority = 3`, f.taskID))
}

func TestApply_ExtraPayload(t *testing.T) {
	f := newPriorityFixture(t)
	title := "Renamed"
	res, err := f.apply(taskupdate.Patch{Title: &title}, map[string]any{
		"proposalId": "01900000-0000-7000-8000-000000000001",
		"runKey":     "run-1",
	})
	require.NoError(t, err)
	require.Equal(t, f.pub.String(), res.Payload["taskId"])
	require.Equal(t, "run-1", res.Payload["runKey"])

	require.Equal(t, 1, f.count(
		`SELECT COUNT(*) FROM events WHERE task_id = ? AND type = 'task.updated'
AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.runKey')) = 'run-1'
AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.proposalId')) = '01900000-0000-7000-8000-000000000001'`, f.taskID))
}
