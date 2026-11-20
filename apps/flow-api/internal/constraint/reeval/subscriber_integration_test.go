package reeval_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint/reeval"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// TestSubscriberSkipsEventWithoutTask runs the job for an event with no
// task_id and checks it evaluates nothing: the workspace's unevaluated
// constraint keeps NULL markers and no verdict event is appended.
func TestSubscriberSkipsEventWithoutTask(t *testing.T) {
	testhelpers.SkipUnlessIntegration(t)
	db := helpers.StartShared(t).DB
	ctx := context.Background()

	wsID, wsPublicID, constraintID := seedTaskWithConstraint(t, db)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, db, wsPublicID.String()) })

	require.NoError(t, eventbus.Append(ctx, dbretry.AutoCommit(db), eventbus.Event{
		Type:        eventbus.LabelCreated,
		WorkspaceID: wsID,
		Payload:     map[string]any{},
	}))
	var eventID uint64
	var taskID sql.NullInt64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT id, task_id FROM events WHERE workspace_id = ? ORDER BY id DESC LIMIT 1`, wsID).Scan(&eventID, &taskID))
	require.False(t, taskID.Valid, "the fixture event must not be task-scoped")

	jobs := 0
	sub := &reeval.Subscriber{
		DB: db,
		Go: func(ctx context.Context, _ uint32, job func(context.Context)) bool {
			jobs++
			job(ctx)
			return true
		},
	}
	sub.Hook()(ctx, wsID, string(eventbus.LabelCreated), eventID)
	require.Equal(t, 1, jobs, "a non-verdict event must schedule exactly one job")

	var satisfiedAt, failedAt sql.NullTime
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT satisfied_at, failed_at FROM task_constraints WHERE public_id = ?`, constraintID).Scan(&satisfiedAt, &failedAt))
	require.False(t, satisfiedAt.Valid, "no constraint may be evaluated for an event without a task")
	require.False(t, failedAt.Valid, "no constraint may be evaluated for an event without a task")

	var verdicts int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type IN (?, ?)`,
		wsID, string(eventbus.TaskConstraintSatisfied), string(eventbus.TaskConstraintFailed)).Scan(&verdicts))
	require.Zero(t, verdicts, "no verdict event may be appended for an event without a task")
}

// seedTaskWithConstraint inserts a workspace with one task carrying one
// never-evaluated constraint.
func seedTaskWithConstraint(t *testing.T, db *sql.DB) (uint32, types.PublicID, types.PublicID) {
	t.Helper()
	ctx := context.Background()
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	wsPublicID := types.New()
	constraintID := types.New()
	insertID := func(query string, args ...any) uint32 {
		res, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err, "seed exec: %s", query)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		return uint32(id) //#nosec G115 -- test fixture IDs are INT UNSIGNED
	}

	wsID := insertID(`INSERT INTO workspaces (public_id, slug, name, timezone)
VALUES (?, ?, ?, 'UTC')`, wsPublicID, "rev-"+suffix, "Reeval Subscriber")
	userID := insertID(`INSERT INTO users (public_id, email, display_name, locale, timezone)
VALUES (?, ?, ?, 'en', 'UTC')`, types.New(), "reeval+"+suffix+"@example.test", "Reeval User")
	projectID := insertID(`INSERT INTO projects (public_id, workspace_id, name, slug)
VALUES (?, ?, ?, ?)`, types.New(), wsID, "Reeval Project", "revp-"+suffix)
	taskID := insertID(`INSERT INTO tasks (
  public_id, workspace_id, project_id, created_by_user_id, task_number, title
) VALUES (?, ?, ?, ?, 1, 'Reeval task')`, types.New(), wsID, projectID, userID)
	insertID(`INSERT INTO task_constraints (public_id, workspace_id, task_id, kind, expression)
VALUES (?, ?, ?, 'approval', ?)`, constraintID, wsID, taskID, `{"op":"actor.has_role","arg":"reviewer"}`)
	return wsID, wsPublicID, constraintID
}
