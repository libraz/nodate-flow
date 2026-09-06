package calendar

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// publicIDFromString parses a UUID string into a BINARY(16) PublicID,
// matching how the DB stores event public IDs.
func publicIDFromString(t *testing.T, s string) dbtype.PublicID {
	t.Helper()
	uid, err := uuid.Parse(s)
	require.NoError(t, err)
	return dbtype.FromUUID(uid)
}

// seedTaskWithDueOn inserts a project + task directly so the tests can
// exercise the cross-table writes the calendar handlers perform when an
// event is linked to a task.
func seedTaskWithDueOn(t *testing.T, tt *helpers.CalendarTestTenant, dueOn *time.Time) (string, uint32) {
	t.Helper()
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	projectPub := dbtype.New()
	var projectID uint32
	res, err := testDB.ExecContext(ctx,
		`INSERT INTO projects (public_id, workspace_id, name, slug, identifier)
		 VALUES (?, ?, ?, ?, ?)`,
		projectPub, tt.WorkspaceID,
		"TaskSync Test "+suffix, "pj-"+suffix[:10], "TSK",
	)
	require.NoError(t, err, "insert project")
	id64, err := res.LastInsertId()
	require.NoError(t, err)
	projectID = uint32(id64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	taskPub := dbtype.New()
	var dueOnNT sql.NullTime
	if dueOn != nil {
		dueOnNT = sql.NullTime{Time: *dueOn, Valid: true}
	}
	res, err = testDB.ExecContext(ctx,
		`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, due_on)
		 VALUES (?, ?, ?, 1, ?, 'public', ?)`,
		taskPub, tt.WorkspaceID, projectID, "Sync Me", dueOnNT,
	)
	require.NoError(t, err, "insert task")
	id64, err = res.LastInsertId()
	require.NoError(t, err)
	return taskPub.String(), uint32(id64) //#nosec G115 -- LastInsertId in test seed, fits uint32
}

// TestCreateEventFromTaskLinksTaskRole verifies the POST
// /events/from-task handler routes through itemkit.ScheduleTask and
// writes task_id + task_role = 'due' on the new calendar_events row.
func TestCreateEventFromTaskLinksTaskRole(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	dueOn := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	taskPubStr, taskInternal := seedTaskWithDueOn(t, tt, &dueOn)

	var resp struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events", "from-task"),
		tt.AccessToken,
		map[string]any{"taskId": taskPubStr, "timezone": "UTC"},
		&resp,
	)
	require.NotEmpty(t, resp.ID)

	var gotTaskID sql.NullInt32
	var gotTaskRole sql.NullString
	err := testDB.QueryRowContext(context.Background(),
		`SELECT task_id, task_role FROM calendar_events
		 WHERE public_id = ? AND workspace_id = ?`,
		publicIDFromString(t, resp.ID), tt.WorkspaceID,
	).Scan(&gotTaskID, &gotTaskRole)
	require.NoError(t, err)
	require.True(t, gotTaskID.Valid, "task_id must be set on linked event")
	assert.Equal(t, int32(taskInternal), gotTaskID.Int32) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits int32 in test seed
	require.True(t, gotTaskRole.Valid)
	assert.Equal(t, "due", gotTaskRole.String)
}

// TestPatchLinkedEventPropagatesToTask verifies the PATCH /events/{evt}
// handler routes through itemkit.RescheduleEvent when the event is
// linked, so tasks.due_on mirrors the new DATE portion.
func TestPatchLinkedEventPropagatesToTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	initialDate := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	taskPubStr, taskInternal := seedTaskWithDueOn(t, tt, &initialDate)

	var createResp struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events", "from-task"),
		tt.AccessToken,
		map[string]any{"taskId": taskPubStr, "timezone": "UTC"},
		&createResp,
	)
	require.NotEmpty(t, createResp.ID)

	newStart := time.Date(2026, 6, 15, 14, 30, 0, 0, time.UTC)
	newEnd := newStart.Add(time.Hour)
	helpers.DoJSON(t, http.MethodPatch,
		tt.WsPath("calendars", calID, "events", createResp.ID),
		tt.AccessToken,
		map[string]any{
			"startAt": newStart.Unix(),
			"endAt":   newEnd.Unix(),
		},
		nil,
	)

	var gotDueOn sql.NullTime
	err := testDB.QueryRowContext(context.Background(),
		`SELECT due_on FROM tasks WHERE id = ?`, taskInternal,
	).Scan(&gotDueOn)
	require.NoError(t, err)
	require.True(t, gotDueOn.Valid, "due_on must stay set after reschedule")
	assert.Equal(t, "2026-06-15", gotDueOn.Time.Format("2006-01-02"))
}

// TestDeleteLinkedEventClearsTaskColumn verifies the DELETE /events/{evt}
// handler routes through itemkit.DeleteEvent so tasks.due_on becomes
// NULL while the task row itself survives.
func TestDeleteLinkedEventClearsTaskColumn(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	initialDate := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	taskPubStr, taskInternal := seedTaskWithDueOn(t, tt, &initialDate)

	var createResp struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost,
		tt.WsPath("calendars", calID, "events", "from-task"),
		tt.AccessToken,
		map[string]any{"taskId": taskPubStr, "timezone": "UTC"},
		&createResp,
	)

	helpers.DoJSON(t, http.MethodDelete,
		tt.WsPath("calendars", calID, "events", createResp.ID),
		tt.AccessToken, nil, nil,
	)

	var gotDueOn sql.NullTime
	var gotEnabled bool
	err := testDB.QueryRowContext(context.Background(),
		`SELECT due_on, enabled FROM tasks WHERE id = ?`, taskInternal,
	).Scan(&gotDueOn, &gotEnabled)
	require.NoError(t, err)
	assert.False(t, gotDueOn.Valid, "due_on must be cleared after event delete")
	assert.True(t, gotEnabled, "task itself must remain enabled")

	var evtEnabled bool
	err = testDB.QueryRowContext(context.Background(),
		`SELECT enabled FROM calendar_events WHERE public_id = ?`,
		publicIDFromString(t, createResp.ID),
	).Scan(&evtEnabled)
	require.NoError(t, err)
	assert.False(t, evtEnabled)
}

// seedPrivateTaskOfOwner inserts a private task in a fresh project,
// created by the workspace owner and with no task actors, so a second
// member of the same workspace is outside every branch of the Layer 4
// rule for it. Returns the task's public id and its title.
func seedPrivateTaskOfOwner(t *testing.T, owner *helpers.CalendarTestTenant) (string, string) {
	t.Helper()
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	projectPub := dbtype.New()
	res, err := testDB.ExecContext(ctx,
		`INSERT INTO projects (public_id, workspace_id, name, slug, identifier)
		 VALUES (?, ?, ?, ?, ?)`,
		projectPub, owner.WorkspaceID,
		"TaskSync Private "+suffix, "pv-"+suffix[:10], "PVT",
	)
	require.NoError(t, err, "insert project")
	id64, err := res.LastInsertId()
	require.NoError(t, err)
	projectID := uint32(id64) //#nosec G115 -- LastInsertId in test seed, fits uint32

	title := "private task title " + suffix
	taskPub := dbtype.New()
	_, err = testDB.ExecContext(ctx,
		`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, created_by_user_id)
		 VALUES (?, ?, ?, 1, ?, 'private', ?)`,
		taskPub, owner.WorkspaceID, projectID, title, owner.UserInternalID,
	)
	require.NoError(t, err, "insert private task")
	return taskPub.String(), title
}

// TestCreateEventFromTaskRefusesTaskTheCallerCannotSee holds the
// task-to-calendar sync to the Layer 4 rule.
//
// The task is named in the request body rather than the path, so no
// RequireTaskAccess runs for it and workspace membership alone gets the
// caller to the handler. Without the rule in the lookup, naming another
// member's private task answers with its title and copies that title
// into a calendar event, which is a second and durable disclosure.
//
// The caller is the extra member rather than the owner on purpose: a
// workspace admin or owner bypasses the predicate, so the same request
// made as the owner would succeed whether the rule were applied or not.
func TestCreateEventFromTaskRefusesTaskTheCallerCannotSee(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	calID := createCalendar(t, owner)
	calInternalID := helpers.ResolveCalendarInternalID(t, testDB, calID)
	member := helpers.CreateExtraCalendarMember(t, testSrv, owner, calInternalID, "editor")

	taskPubStr, title := seedPrivateTaskOfOwner(t, owner)

	status, body := helpers.DoJSONStatus(t, http.MethodPost,
		member.WsPath("calendars", calID, "events", "from-task"),
		member.AccessToken,
		map[string]any{"taskId": taskPubStr, "timezone": "UTC"},
	)
	// The refusal is the not-found code, not a distinct "forbidden": a
	// caller who may not see the task may not learn that it exists.
	assert.Equal(t, http.StatusNotFound, status)
	assert.Contains(t, string(body), "CALENDAR.TASK_SYNC.TASK_NOT_FOUND")
	assert.NotContains(t, string(body), title,
		"the refusal must not carry the title it refused to disclose")

	// The title must not have reached a calendar event either. The
	// response alone is not the whole disclosure: a row written here is
	// readable by everyone on the calendar afterwards.
	var leaked int
	err := testDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM calendar_events WHERE workspace_id = ? AND title = ?`,
		owner.WorkspaceID, title,
	).Scan(&leaked)
	require.NoError(t, err)
	assert.Zero(t, leaked, "no calendar event may carry the private task's title")

	// The owner may still sync their own private task, so the rule is
	// what refused the member rather than the lookup having broken.
	var resp struct {
		ID string `json:"id"`
	}
	helpers.DoJSON(t, http.MethodPost,
		owner.WsPath("calendars", calID, "events", "from-task"),
		owner.AccessToken,
		map[string]any{"taskId": taskPubStr, "timezone": "UTC"},
		&resp,
	)
	assert.NotEmpty(t, resp.ID, "the task's own creator must still be able to sync it")
}
