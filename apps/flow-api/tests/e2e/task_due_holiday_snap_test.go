package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// setSnapPreferences writes the actor's scheduling preferences straight to
// the row. The profile PATCH that owns these fields lives in auth-api,
// which this suite does not run; what is under test is what the flow api
// does with them, not how they get set.
func setSnapPreferences(t *testing.T, userPublicID, country, mode string, treatHolidays bool) {
	t.Helper()
	_, err := testDB.Exec(
		`UPDATE users
		    SET country = ?, snap_to_working_day = ?, treat_holidays_as_non_working = ?
		  WHERE public_id = UUID_TO_BIN(?, 0)`,
		country, mode, treatHolidays, userPublicID)
	require.NoError(t, err)
}

// linkTaskToCalendar projects the task onto the tenant's own calendar.
// Due-date snapping runs on the task/event pair, so a task with no
// calendar projection never reaches the snap path at all — the fixture
// creates the link the product's calendar view creates.
func linkTaskToCalendar(t *testing.T, tt *helpers.TestTenant, taskID string) {
	t.Helper()
	var cal struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars",
		tt.AccessToken,
		map[string]any{"kind": "personal", "name": "Snap fixture", "color": "#4285F4"},
		&cal)
	require.NotEmpty(t, cal.ID)
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/calendars/"+cal.ID+"/events/from-task",
		tt.AccessToken, map[string]any{"taskId": taskID, "timezone": "UTC"}, nil)
}

// patchDueOn sets a task's due date and returns the date that ended up in
// the column. The stored value is read back rather than taken from the
// response so the assertion is about what the server persisted.
func patchDueOn(t *testing.T, token, taskID, dueOn string) string {
	t.Helper()
	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+taskID, token,
		map[string]any{"dueOn": dueOn}, nil)
	var stored string
	require.NoError(t, testDB.QueryRow(
		`SELECT DATE_FORMAT(due_on, '%Y-%m-%d') FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)`,
		taskID).Scan(&stored))
	return stored
}

// TestDueDateSnapsOffAPublicHoliday is the end-to-end regression for the
// holiday half of working-day snapping.
//
// users.treat_holidays_as_non_working was readable, settable, and
// completely inert: the snap config carried the flag but its holiday set
// was never populated by anything, so a deadline on New Year's Day was
// treated as an ordinary working Thursday. The country's holidays are now
// resolved server-side, which is the only place they can be resolved —
// the browser cannot decide where a due date lands.
//
// 6 May 2026 is a Wednesday and a Japanese public holiday (the observed
// day for Children's Day), so it is non-working for exactly one reason:
// the holiday. Weekend logic cannot produce this result.
func TestDueDateSnapsOffAPublicHoliday(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Ship the release")
	linkTaskToCalendar(t, tt, taskID)

	setSnapPreferences(t, tt.UserPublicID, "JP", "auto", true)
	require.Equal(t, "2026-05-07", patchDueOn(t, tt.AccessToken, taskID, "2026-05-06"),
		"a due date on a Japanese public holiday must move to the next working day")

	// An ordinary Wednesday is left alone, so the move above is
	// attributable to the holiday and not to snapping in general.
	require.Equal(t, "2026-06-10", patchDueOn(t, tt.AccessToken, taskID, "2026-06-10"),
		"a due date on an ordinary working Wednesday must not move")
}

// TestDueDateHolidaySnapHonoursTheUserSetting pins the opt-out: the
// setting has to change the outcome, otherwise it is decoration again.
func TestDueDateHolidaySnapHonoursTheUserSetting(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Ship the other release")
	linkTaskToCalendar(t, tt, taskID)

	setSnapPreferences(t, tt.UserPublicID, "JP", "auto", false)
	require.Equal(t, "2026-05-06", patchDueOn(t, tt.AccessToken, taskID, "2026-05-06"),
		"with treat_holidays_as_non_working off, a holiday is an ordinary working day")
}

// TestDueDateHolidaysFollowTheActorsCountry keeps the holiday set tied to
// the actor rather than to whichever country happened to be compiled in:
// 6 May is a working day in the United States.
func TestDueDateHolidaysFollowTheActorsCountry(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskID := createTaskForAgent(t, tt, "Ship the US release")
	linkTaskToCalendar(t, tt, taskID)

	setSnapPreferences(t, tt.UserPublicID, "US", "auto", true)
	require.Equal(t, "2026-05-06", patchDueOn(t, tt.AccessToken, taskID, "2026-05-06"),
		"6 May is not a US public holiday, so a US actor's deadline must stay put")

	// Independence Day 2026 falls on a Saturday, so pick a weekday one:
	// Thanksgiving, 26 November 2026, a Thursday.
	require.Equal(t, "2026-11-27", patchDueOn(t, tt.AccessToken, taskID, "2026-11-26"),
		"a due date on Thanksgiving must move to the next working day for a US actor")
}
