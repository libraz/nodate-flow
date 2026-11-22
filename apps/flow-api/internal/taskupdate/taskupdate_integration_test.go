package taskupdate_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskupdate"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// TestTaskUpdateApply runs the kit against a real database: the row, the
// description history and the timeline event land together, and a refusal
// leaves none of them behind.
func TestTaskUpdateApply(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst, err := helpers.EnsureShared()
	require.NoError(t, err, "start shared MySQL")
	db := inst.DB

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

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
	wsID := seed(`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`,
		wsPub, "tu-"+suffix, "Taskupdate "+suffix)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, db, wsPub.String()) })
	userID := seed(`INSERT INTO users (public_id, email, display_name, locale, timezone) VALUES (?, ?, ?, 'en', 'UTC')`,
		types.New(), "taskupdate+"+suffix+"@example.test", "Taskupdate "+suffix)
	projectID := seed(`INSERT INTO projects (public_id, workspace_id, name, slug) VALUES (?, ?, ?, ?)`,
		types.New(), wsID, "Taskupdate Project", "tu-"+suffix)
	taskNumber := 0
	newTask := func() (uint32, types.PublicID) {
		taskNumber++
		pub := types.New()
		id := seed(`INSERT INTO tasks (public_id, workspace_id, project_id, created_by_user_id, task_number, title, priority, visibility, due_on, started_on)
VALUES (?, ?, ?, ?, ?, 'Original', 1, 'public', '2026-12-10', '2026-12-01')`, pub, wsID, projectID, userID, taskNumber)
		return id, pub
	}
	actor := mutationlog.Actor{UserID: userID, WorkspaceID: wsID}
	apply := func(taskID uint32, pub types.PublicID, patch taskupdate.Patch) (taskupdate.Result, error) {
		var (
			res      taskupdate.Result
			applyErr error
		)
		_ = dbretry.InTx(ctx, db, "test.TaskUpdateApply", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			res, applyErr = taskupdate.Apply(ctx, tx, taskupdate.Args{
				WorkspaceID: wsID,
				TaskID:      taskID,
				PublicID:    pub,
				Patch:       patch,
				Actor:       actor,
			})
			return applyErr
		})
		return res, applyErr
	}
	count := func(query string, args ...any) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, query, args...).Scan(&n))
		return n
	}
	updatedEvents := func(taskID uint32) int {
		return count(`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND task_id = ? AND type = 'task.updated'`, wsID, taskID)
	}
	versions := func(taskID uint32) int {
		return count(`SELECT COUNT(*) FROM task_description_versions WHERE task_id = ?`, taskID)
	}
	date := func(s string) *sql.NullTime {
		d, err := time.Parse(time.DateOnly, s)
		require.NoError(t, err)
		return &sql.NullTime{Time: d, Valid: true}
	}
	str := func(s string) *string { return &s }

	t.Run("title change", func(t *testing.T) {
		taskID, pub := newTask()
		res, err := apply(taskID, pub, taskupdate.Patch{Title: str("Renamed")})
		require.NoError(t, err)
		require.True(t, res.TitleChanged)
		require.False(t, res.DescChanged)
		require.Equal(t, "Renamed", res.NewTitle.String())
		require.Equal(t, pub.String(), res.Payload["taskId"])
		require.Equal(t, 1, count(`SELECT COUNT(*) FROM tasks WHERE id = ? AND title = 'Renamed' AND updated_by_user_id = ?`, taskID, userID))
		require.Equal(t, 1, count(`SELECT COUNT(*) FROM events WHERE task_id = ? AND type = 'task.updated' AND actor_user_id = ?`, taskID, userID))
	})

	t.Run("description change creates version", func(t *testing.T) {
		taskID, pub := newTask()
		res, err := apply(taskID, pub, taskupdate.Patch{Description: str("A new body")})
		require.NoError(t, err)
		require.True(t, res.DescChanged)
		require.Equal(t, "A new body", res.NewDesc.String)
		require.NotZero(t, res.Version.Number)
		require.Equal(t, 1, versions(taskID))
		require.Equal(t, 1, count(`SELECT COUNT(*) FROM events WHERE task_id = ? AND type = 'description.version.created'`, taskID))
		require.Equal(t, 1, updatedEvents(taskID))

		// Re-sending the stored body is not a new version.
		res, err = apply(taskID, pub, taskupdate.Patch{Description: str("A new body")})
		require.NoError(t, err)
		require.False(t, res.DescChanged)
		require.Equal(t, 1, versions(taskID))
	})

	t.Run("date order violation", func(t *testing.T) {
		taskID, pub := newTask()
		// Moves only the due date, to before the stored start.
		_, err := apply(taskID, pub, taskupdate.Patch{DueOn: date("2026-11-20")})
		require.Equal(t, taskrules.ViolationDueBeforeStart, taskrules.Classify(err), "err = %v", err)
		require.Zero(t, updatedEvents(taskID))
		require.Equal(t, 1, count(`SELECT COUNT(*) FROM tasks WHERE id = ? AND due_on = '2026-12-10'`, taskID))
	})

	t.Run("expected due on conflict", func(t *testing.T) {
		taskID, pub := newTask()
		_, err := apply(taskID, pub, taskupdate.Patch{Title: str("Not written"), ExpectedDueOn: date("2026-12-11")})
		require.True(t, errors.Is(err, taskupdate.ErrDueOnConflict), "err = %v", err)
		require.Zero(t, updatedEvents(taskID))
		require.Equal(t, 1, count(`SELECT COUNT(*) FROM tasks WHERE id = ? AND title = 'Original'`, taskID))

		// The matching date lets the same patch through.
		res, err := apply(taskID, pub, taskupdate.Patch{Title: str("Written"), ExpectedDueOn: date("2026-12-10")})
		require.NoError(t, err)
		require.True(t, res.TitleChanged)
	})

	t.Run("no-op patch", func(t *testing.T) {
		taskID, pub := newTask()
		res, err := apply(taskID, pub, taskupdate.Patch{})
		require.NoError(t, err)
		require.False(t, res.TitleChanged)
		require.False(t, res.DescChanged)
		require.Equal(t, "Original", res.NewTitle.String())
		require.Zero(t, versions(taskID))
		require.Equal(t, 1, updatedEvents(taskID))
	})
}
