package taskactors_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskactors"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

func TestTaskActors(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	inst, err := helpers.EnsureShared()
	require.NoError(t, err)
	db := inst.DB
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	wsPub, userPub, taskPub := types.New(), types.New(), types.New()
	execID := func(query string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- fixture ids are small positive values
	}
	wsID := execID(`INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`, wsPub, "ta-"+suffix[len(suffix)-12:], "Actors "+suffix)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, db, wsPub.String()) })
	userID := execID(`INSERT INTO users (public_id, email, display_name, locale, timezone) VALUES (?, ?, ?, 'en', 'UTC')`,
		userPub, "actors+"+suffix+"@example.test", "Actors "+suffix)
	projectID := execID(`INSERT INTO projects (public_id, workspace_id, name, slug) VALUES (?, ?, ?, ?)`, types.New(), wsID, "P", "ta-"+suffix[len(suffix)-12:])
	taskID := execID(`INSERT INTO tasks (public_id, workspace_id, project_id, created_by_user_id, task_number, title, priority, visibility)
VALUES (?, ?, ?, ?, 1, 'Actors fixture', 1, 'public')`, taskPub, wsID, projectID, userID)

	actor := mutationlog.Actor{UserID: userID, WorkspaceID: wsID}
	recorder := mutationlog.New(db, generated.New(db))
	rowPub := types.New()
	args := taskactors.Args{
		Actor: actor, TaskID: taskID, TaskPublicID: taskPub.String(),
		UserID: userID, UserPublicID: userPub.String(),
		Role: generated.TaskActorsRoleReviewer, PublicID: rowPub, Mutations: recorder,
	}
	inTx := func(fn func(*dbretry.Tx) error) error {
		return dbretry.InTx(ctx, db, "taskactors.test", nil, func(_ context.Context, tx *dbretry.Tx) error { return fn(tx) })
	}
	eventCount := func(kind string) int {
		var n int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND task_id = ? AND type = ?`, wsID, taskID, kind).Scan(&n))
		return n
	}

	require.NoError(t, inTx(func(tx *dbretry.Tx) error { return taskactors.Add(ctx, tx, args) }))
	require.Equal(t, 1, eventCount("task.actor.added"))

	err = inTx(func(tx *dbretry.Tx) error { return taskactors.Add(ctx, tx, args) })
	require.True(t, errors.Is(err, taskactors.ErrAlreadyActor))
	require.Equal(t, 1, eventCount("task.actor.added"), "a refused duplicate appends nothing")

	// Another workspace cannot remove the row.
	other := args
	other.Actor.WorkspaceID = wsID + 1_000_000
	other.ActorPublicID = rowPub
	err = inTx(func(tx *dbretry.Tx) error { return taskactors.Remove(ctx, tx, other) })
	require.True(t, errors.Is(err, taskactors.ErrNotActor))

	args.ActorPublicID = rowPub
	require.NoError(t, inTx(func(tx *dbretry.Tx) error { return taskactors.Remove(ctx, tx, args) }))
	require.Equal(t, 1, eventCount("task.actor.removed"))

	err = inTx(func(tx *dbretry.Tx) error { return taskactors.Remove(ctx, tx, args) })
	require.True(t, errors.Is(err, taskactors.ErrNotActor))
	require.Equal(t, 1, eventCount("task.actor.removed"), "a refused removal appends nothing")

	var enabled sql.NullBool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT enabled FROM task_actors WHERE task_id = ? AND user_id = ?`, taskID, userID).Scan(&enabled))
	require.True(t, enabled.Valid && !enabled.Bool)
}
