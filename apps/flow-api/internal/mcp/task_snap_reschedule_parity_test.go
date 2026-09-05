package mcp_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/tasks"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
)

// Moving a task's due date is one change with two entrances: the MCP
// tool and the request operation it answers for. The actor's snap
// preference decides where the date lands, so a path that does not
// resolve it stores a different row for the same instruction — and
// neither side reports anything wrong, because both succeed.
//
// The property is therefore parity of the stored row, not the presence
// of a snap call. Both tasks below are seeded identically, both are
// moved to the same non-working day, and the two rows are compared to
// each other as well as to the date the actor's own configuration
// implies.

const snapParityDateLayout = "2006-01-02"

// snapParityFixture is one tenant whose actor snaps forward
// automatically, holding two identical tasks: one moved through the
// tool, one through the request handler.
type snapParityFixture struct {
	wsID   uint32
	userID uint32

	toolTaskPub uuid.UUID
	toolTaskID  uint32
	restTaskPub uuid.UUID
	restTaskID  uint32
}

func seedSnapParityFixture(t *testing.T, db *sql.DB) *snapParityFixture {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.New().String()[:8]

	fx := &snapParityFixture{}
	var calID uint32

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	lastID := func(res sql.Result) uint32 {
		id, idErr := res.LastInsertId()
		require.NoError(t, idErr)
		return uint32(id) //#nosec G115 -- LastInsertId in test seed, fits uint32
	}

	// snap_to_working_day is set explicitly because the column's own
	// default is 'warn', which badges without moving the date — the two
	// paths would then agree even with the resolution missing on one of
	// them, and the comparison would prove nothing.
	userPub := uuid.Must(uuid.NewV7())
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (public_id, email, display_name, locale, timezone, working_days, snap_to_working_day, treat_holidays_as_non_working)
		 VALUES (?, ?, ?, 'en', 'UTC', 'MTWTF__', 'auto', FALSE)`,
		userPub[:], "snapparity-"+suffix+"@example.test", "SnapParity "+suffix)
	require.NoError(t, err)
	fx.userID = lastID(res)

	wsPub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO workspaces (public_id, slug, name, timezone, working_days) VALUES (?, ?, ?, 'UTC', 'MTWTF__')`,
		wsPub[:], "snapparity-ws-"+suffix, "SnapParity Workspace")
	require.NoError(t, err)
	fx.wsID = lastID(res)

	memberPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, 'owner')`,
		memberPub[:], fx.wsID, fx.userID)
	require.NoError(t, err)

	prjPub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO projects (public_id, workspace_id, slug, name, identifier) VALUES (?, ?, ?, ?, ?)`,
		prjPub[:], fx.wsID, "snapparity-prj-"+suffix, "SnapParity Project", "SP"+suffix[:3])
	require.NoError(t, err)
	prjID := lastID(res)

	calPub := uuid.Must(uuid.NewV7())
	res, err = tx.ExecContext(ctx,
		`INSERT INTO calendars (public_id, workspace_id, kind, name, owner_user_id) VALUES (?, ?, 'personal', ?, ?)`,
		calPub[:], fx.wsID, "SnapParity Calendar", fx.userID)
	require.NoError(t, err)
	calID = lastID(res)

	calMemberPub := uuid.Must(uuid.NewV7())
	_, err = tx.ExecContext(ctx,
		`INSERT INTO calendar_members (public_id, workspace_id, calendar_id, user_id, role) VALUES (?, ?, ?, ?, 'owner')`,
		calMemberPub[:], fx.wsID, calID, fx.userID)
	require.NoError(t, err)

	newTask := func(number int, title string) (uuid.UUID, uint32) {
		pub := uuid.Must(uuid.NewV7())
		created, terr := tx.ExecContext(ctx,
			`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, created_by_user_id)
			 VALUES (?, ?, ?, ?, ?, 'public', ?)`,
			pub[:], fx.wsID, prjID, number, title, fx.userID)
		require.NoError(t, terr)
		return pub, lastID(created)
	}
	fx.toolTaskPub, fx.toolTaskID = newTask(1, "Moved by the tool")
	fx.restTaskPub, fx.restTaskID = newTask(2, "Moved by the request")

	require.NoError(t, tx.Commit())
	committed = true

	// Both tasks get a projection event on the same working day, at the
	// same time of day, for the same duration. Anything that differed
	// here would show up later as a difference the two paths did not
	// cause.
	seedStart := snapParityWeekday(t, time.Now().UTC().AddDate(0, 0, 14))
	seedStart = time.Date(seedStart.Year(), seedStart.Month(), seedStart.Day(), 9, 0, 0, 0, time.UTC)
	for _, task := range []struct {
		id    uint32
		title string
	}{
		{fx.toolTaskID, "Moved by the tool"},
		{fx.restTaskID, "Moved by the request"},
	} {
		require.NoError(t, dbretry.InTx(ctx, db, "test.seed_snap_projection", nil,
			func(ctx context.Context, tx *dbretry.Tx) error {
				_, _, serr := itemkit.ScheduleTask(ctx, tx, itemkit.ScheduleTaskArgs{
					WorkspaceID: fx.wsID,
					TaskID:      task.id,
					CalendarID:  calID,
					ActorUserID: fx.userID,
					Role:        itemkit.RoleDue,
					Title:       task.title,
					StartAt:     seedStart,
					EndAt:       seedStart.Add(time.Hour),
					Timezone:    "UTC",
				})
				return serr
			}))
	}

	return fx
}

// snapParityWeekday returns the first Monday-to-Friday day on or after d.
func snapParityWeekday(t *testing.T, d time.Time) time.Time {
	t.Helper()
	for i := 0; i < 7; i++ {
		switch d.Weekday() {
		case time.Saturday, time.Sunday:
			d = d.AddDate(0, 0, 1)
		default:
			return d
		}
	}
	t.Fatal("no weekday found within a week, which is not possible")
	return time.Time{}
}

// snapParityStoredRow is everything the two paths write between them:
// the task's own date column and the projection event that mirrors it.
type snapParityStoredRow struct {
	DueOn   string
	StartAt time.Time
	EndAt   time.Time
	Flags   string
}

func snapParityReadStored(t *testing.T, db *sql.DB, taskID uint32) snapParityStoredRow {
	t.Helper()
	var due sql.NullTime
	require.NoError(t, db.QueryRow(`SELECT due_on FROM tasks WHERE id = ?`, taskID).Scan(&due))

	var (
		start, end sql.NullTime
		flags      sql.NullString
	)
	require.NoError(t, db.QueryRow(
		`SELECT start_at, end_at, flags FROM calendar_events
		 WHERE task_id = ? AND task_role = 'due' AND enabled = TRUE`,
		taskID).Scan(&start, &end, &flags))

	out := snapParityStoredRow{Flags: flags.String}
	if due.Valid {
		out.DueOn = due.Time.Format(snapParityDateLayout)
	}
	out.StartAt, out.EndAt = start.Time, end.Time
	return out
}

// snapParityRequestContext runs the task ACL middleware over a request
// naming the task, and hands back the context it built.
//
// The request handler reads its workspace, actor and task from that
// context and from nowhere else, so this is what makes it callable
// outside a running server. Building the values directly is not an
// option: the context keys are unexported, on purpose.
func snapParityRequestContext(t *testing.T, db *sql.DB, actorID uint32, taskPub uuid.UUID) context.Context {
	t.Helper()
	var captured context.Context
	router := chi.NewRouter()
	router.With(middleware.RequireTaskAccess(db)).Get("/tasks/{id}", func(_ http.ResponseWriter, r *http.Request) {
		captured = r.Context()
	})

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskPub.String(), nil)
	req = req.WithContext(middleware.WithActor(req.Context(), actorID))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equalf(t, http.StatusOK, rec.Code,
		"the task ACL middleware refused the actor (%s); the request path was never entered", rec.Body.String())
	require.NotNil(t, captured, "the middleware chain did not reach the handler")
	return captured
}

// TestTaskRescheduleThroughToolAndRequestStoreTheSameRow moves the same
// due date, on the same day, for the same actor, through both entrances
// and compares what each one left behind.
func TestTaskRescheduleThroughToolAndRequestStoreTheSameRow(t *testing.T) {
	requireMCPIntegration(t)
	inst := helpers.StartShared(t)
	db := inst.DB
	fx := seedSnapParityFixture(t, db)

	ctx := context.Background()
	queries := generated.New(db)

	// The actor's configuration, read the way both paths read it, so the
	// expected date below is derived rather than restated.
	var snapCfg itemkit.SnapConfig
	require.NoError(t, dbretry.InTx(ctx, db, "test.resolve_snap", nil,
		func(ctx context.Context, tx *dbretry.Tx) error {
			var rerr error
			snapCfg, rerr = itemkit.ResolveSnapConfig(ctx, tx, fx.wsID, fx.userID)
			return rerr
		}))
	require.Equal(t, region.SnapAuto, snapCfg.Mode,
		"the actor must actually move dates, or both paths agree whether or not either resolved the configuration")

	// A Saturday far enough out that no run picks a date in the past.
	target := time.Now().UTC().AddDate(0, 0, 30)
	for target.Weekday() != time.Saturday {
		target = target.AddDate(0, 0, 1)
	}
	target = time.Date(target.Year(), target.Month(), target.Day(), 0, 0, 0, 0, time.UTC)
	require.False(t,
		region.IsWorkingDay(snapCfg.WorkingDays, target, snapCfg.Zone, snapCfg.Holidays, snapCfg.TreatHolidays),
		"the target day is a working day for this actor, so nothing would snap and the comparison would be vacuous")

	wantDue := region.NextWorkingDay(
		snapCfg.WorkingDays, target, snapCfg.Zone, snapCfg.Holidays, snapCfg.TreatHolidays,
	).Format(snapParityDateLayout)
	requested := target.Format(snapParityDateLayout)
	require.NotEqual(t, requested, wantDue,
		"the configuration moves nothing on this date; an unresolved snap would be indistinguishable from a resolved one")

	t.Run("tool", func(t *testing.T) {
		deps := mcp.Deps{DB: db, Queries: queries}
		sess := mcp.NewTestSession(fx.userID, fx.wsID, []string{"write:workspace"})
		_, err := mcp.RunUpdateTask(ctx, deps, sess, mcpTrailArgs(t, map[string]any{
			"taskId": fx.toolTaskPub.String(),
			"dueOn":  requested,
		}))
		require.NoError(t, err)
	})

	t.Run("request", func(t *testing.T) {
		reqCtx := snapParityRequestContext(t, db, fx.userID, fx.restTaskPub)
		handler := tasks.Patch(tasks.Deps{DB: db, Queries: queries, Audit: audit.New(queries)})
		due := requested
		_, err := handler(reqCtx, &tasks.PatchTaskInput{
			ID:   fx.restTaskPub.String(),
			Body: tasks.PatchTaskBody{DueOn: &due},
		})
		require.NoError(t, err)
	})

	toolRow := snapParityReadStored(t, db, fx.toolTaskID)
	restRow := snapParityReadStored(t, db, fx.restTaskID)

	require.Equal(t, restRow, toolRow,
		"the same move through the two entrances stored two different rows; whichever one a reader trusts, the other is wrong")
	require.Equal(t, wantDue, toolRow.DueOn,
		"the tool stored %s for a request of %s; the actor's configuration puts it on %s",
		toolRow.DueOn, requested, wantDue)
	require.Equal(t, wantDue, toolRow.StartAt.UTC().Format(snapParityDateLayout),
		"the task moved but its projection event did not follow, so the calendar and the task disagree")
}
