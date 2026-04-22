package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/google/uuid"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/time-api/tests/helpers"
)

// publicIDFromString parses a UUID string into a BINARY(16) PublicID,
// matching how the DB stores event public IDs.
func publicIDFromString(t *testing.T, s string) types.PublicID {
	t.Helper()
	uid, err := uuid.Parse(s)
	require.NoError(t, err)
	return types.FromUUID(uid)
}

// seedTaskWithEventOn inserts a project + task directly so the tests can
// exercise time-api's cross-table writes. time-api has no task endpoints
// by design; tasks belong to flow-api, but schema / rows are shared.
//
// Returns the task's public ID and internal ID.
func seedTaskWithEventOn(t *testing.T, tt *helpers.TestTenant, eventOn *time.Time) (string, uint32) {
	t.Helper()
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	projectPub := types.New()
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
	projectID = uint32(id64)

	taskPub := types.New()
	var eventOnNT sql.NullTime
	if eventOn != nil {
		eventOnNT = sql.NullTime{Time: *eventOn, Valid: true}
	}
	res, err = testDB.ExecContext(ctx,
		`INSERT INTO tasks (public_id, workspace_id, project_id, task_number, title, visibility, event_on)
		 VALUES (?, ?, ?, 1, ?, 'public', ?)`,
		taskPub, tt.WorkspaceID, projectID, "Sync Me", eventOnNT,
	)
	require.NoError(t, err, "insert task")
	id64, err = res.LastInsertId()
	require.NoError(t, err)
	return taskPub.String(), uint32(id64)
}

// TestCreateEventFromTaskLinksTaskRole verifies the POST
// /events/from-task handler routes through itemkit.ScheduleTask and
// writes task_id + task_role = 'event' on the new calendar_events row.
func TestCreateEventFromTaskLinksTaskRole(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	eventOn := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	taskPubStr, taskInternal := seedTaskWithEventOn(t, tt, &eventOn)

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

	// DB-level assertion: linked event has task_id + task_role='event'
	var gotTaskID sql.NullInt32
	var gotTaskRole sql.NullString
	err := testDB.QueryRowContext(context.Background(),
		`SELECT task_id, task_role FROM calendar_events
		 WHERE public_id = ? AND workspace_id = ?`,
		publicIDFromString(t, resp.ID), tt.WorkspaceID,
	).Scan(&gotTaskID, &gotTaskRole)
	require.NoError(t, err)
	require.True(t, gotTaskID.Valid, "task_id must be set on linked event")
	assert.Equal(t, int32(taskInternal), gotTaskID.Int32)
	require.True(t, gotTaskRole.Valid)
	assert.Equal(t, "event", gotTaskRole.String)
}

// TestPatchLinkedEventPropagatesToTask verifies the PATCH /events/{evt}
// handler routes through itemkit.RescheduleEvent when the event is
// linked, so tasks.event_on mirrors the new DATE portion.
func TestPatchLinkedEventPropagatesToTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	initialDate := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	taskPubStr, taskInternal := seedTaskWithEventOn(t, tt, &initialDate)

	// Create the linked event via from-task.
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

	// Move the event forward by 5 days.
	newStart := time.Date(2026, 6, 15, 14, 30, 0, 0, time.UTC)
	newEnd := newStart.Add(time.Hour)
	helpers.DoJSON(t, http.MethodPatch,
		tt.WsPath("calendars", calID, "events", createResp.ID),
		tt.AccessToken,
		map[string]any{
			"startAt": newStart.Format(time.RFC3339),
			"endAt":   newEnd.Format(time.RFC3339),
		},
		nil,
	)

	// tasks.event_on should now reflect the new DATE component.
	var gotEventOn sql.NullTime
	err := testDB.QueryRowContext(context.Background(),
		`SELECT event_on FROM tasks WHERE id = ?`, taskInternal,
	).Scan(&gotEventOn)
	require.NoError(t, err)
	require.True(t, gotEventOn.Valid, "event_on must stay set after reschedule")
	assert.Equal(t, "2026-06-15", gotEventOn.Time.Format("2006-01-02"))
}

// TestDeleteLinkedEventClearsTaskColumn verifies the DELETE /events/{evt}
// handler routes through itemkit.DeleteEvent so tasks.event_on becomes
// NULL while the task row itself survives.
func TestDeleteLinkedEventClearsTaskColumn(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	calID := createCalendar(t, tt)

	initialDate := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	taskPubStr, taskInternal := seedTaskWithEventOn(t, tt, &initialDate)

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

	var gotEventOn sql.NullTime
	var gotEnabled bool
	err := testDB.QueryRowContext(context.Background(),
		`SELECT event_on, enabled FROM tasks WHERE id = ?`, taskInternal,
	).Scan(&gotEventOn, &gotEnabled)
	require.NoError(t, err)
	assert.False(t, gotEventOn.Valid, "event_on must be cleared after event delete")
	assert.True(t, gotEnabled, "task itself must remain enabled")

	// Event row is soft-disabled, not removed.
	var evtEnabled bool
	err = testDB.QueryRowContext(context.Background(),
		`SELECT enabled FROM calendar_events WHERE public_id = ?`,
		publicIDFromString(t, createResp.ID),
	).Scan(&evtEnabled)
	require.NoError(t, err)
	assert.False(t, evtEnabled)
}
