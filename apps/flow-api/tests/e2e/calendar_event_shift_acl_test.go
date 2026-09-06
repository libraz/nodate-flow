// Authorization suite for the umbrella-event shift routes.
//
// Shifting an event moves its start time and, on apply, the due dates of
// every task confirmed with it. Both are writes to the calendar the event
// lives on, so both take the same standing on that calendar as any other
// change to its contents: a member below editor is refused, and a
// calendar the caller holds no grant on answers as though the event did
// not exist. Workspace membership reaches an event id and nothing more —
// a workspace holds calendars whose audiences do not coincide.
package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// shiftFixtureDue is the linked task's due date. It matches the day
// createEventMut schedules its event on, so a task that moves and an
// event that moves do so by the same delta.
const shiftFixtureDue = "2027-06-01"

// createTaskWithDue creates a task carrying a due date, which is what
// makes it a candidate for a shift: apply moves the DATE columns, so a
// task without one has nothing to move.
func createTaskWithDue(t *testing.T, owner *helpers.TestTenant, title, dueOn string) string {
	t.Helper()
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId": owner.ProjectPublicID,
			"title":     title,
			"dueOn":     dueOn,
		}, &task)
	require.NotEmpty(t, task.ID, "create task must return a public id")
	return task.ID
}

// linkTaskToEventContributesTo writes the contributes_to link the shift
// endpoints walk. The row is written directly because the endpoint that
// creates one applies rules of its own, and what is under test here is
// the shift, not the link.
func linkTaskToEventContributesTo(t *testing.T, taskID, evtID string) {
	t.Helper()
	_, err := testDB.Exec(
		`INSERT INTO task_event_links
		   (public_id, workspace_id, task_id, event_id, relation)
		 SELECT UUID_TO_BIN(UUID(), 0), t.workspace_id, t.id, e.id, 'contributes_to'
		   FROM tasks t, calendar_events e
		  WHERE t.public_id = UUID_TO_BIN(?, 0)
		    AND e.public_id = UUID_TO_BIN(?, 0)`,
		taskID, evtID)
	require.NoError(t, err)
}

// eventStartDay reads the day an event currently starts on, as a string
// so the assertion does not depend on how the driver renders a DATETIME.
func eventStartDay(t *testing.T, evtID string) string {
	t.Helper()
	var day string
	require.NoError(t, testDB.QueryRow(
		`SELECT DATE_FORMAT(start_at, '%Y-%m-%d')
		   FROM calendar_events
		  WHERE public_id = UUID_TO_BIN(?, 0)`,
		evtID).Scan(&day))
	return day
}

// taskDueDay reads a task's due date the same way.
func taskDueDay(t *testing.T, taskID string) string {
	t.Helper()
	var day string
	require.NoError(t, testDB.QueryRow(
		`SELECT DATE_FORMAT(due_on, '%Y-%m-%d')
		   FROM tasks
		  WHERE public_id = UUID_TO_BIN(?, 0)`,
		taskID).Scan(&day))
	return day
}

// TestEventShiftRequiresCalendarWriteAccess walks one workspace member
// through all three standings on the calendar an event lives on.
//
// The member never being added to the calendar is the case that matters:
// they hold a legitimate session in the workspace, so a check that scopes
// the event id to the workspace and stops there is satisfied, and they
// could move a colleague's event — and the due dates of the tasks hanging
// off it — on a calendar they cannot even open. The refusal is a
// not-found rather than a forbidden because the request names an event
// and never names a calendar: telling this caller apart from one who
// supplied an id that matches nothing would confirm the event exists.
//
// The last two legs promote the same user, so a regression that simply
// refuses everybody cannot pass: a viewer is still refused, and an editor
// moves both the event and the linked task.
func TestEventShiftRequiresCalendarWriteAccess(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	outsider := inviteAndJoinWorkspace(t, host)

	calID := createCalendarMut(t, host, "Shift ACL Cal")
	evtID := createEventMut(t, host, calID, "Quarterly planning offsite")
	taskID := createTaskWithDue(t, host, "Book the offsite venue", shiftFixtureDue)
	linkTaskToEventContributesTo(t, taskID, evtID)

	shiftBase := testServerURL + "/workspaces/" + host.WorkspacePublicID +
		"/calendar-events/" + evtID
	newStart := time.Date(2027, 6, 8, 10, 0, 0, 0, time.UTC)

	// No membership at all. Both routes answer as though the event were
	// not there.
	status, body := doJSONStatus(t, http.MethodPost, shiftBase+"/propose-shift",
		outsider.AccessToken, map[string]any{"newStartAt": newStart.Unix()})
	requireDenied(t, status, body, http.StatusNotFound, "CALENDAR.EVENT.NOT_FOUND",
		"propose-shift for a workspace member holding no grant on the calendar")

	status, body = doJSONStatus(t, http.MethodPost, shiftBase+"/apply-shift",
		outsider.AccessToken, map[string]any{
			"newStartAt":       newStart.Unix(),
			"confirmedTaskIds": []string{taskID},
		})
	requireDenied(t, status, body, http.StatusNotFound, "CALENDAR.EVENT.NOT_FOUND",
		"apply-shift for a workspace member holding no grant on the calendar")

	// The refusal has to happen before anything moves. The linked task is
	// the sharper witness of the two: it lives outside the calendar
	// entirely, so a shift that reached it has already escaped the
	// boundary this test is about.
	assert.Equal(t, shiftFixtureDue, taskDueDay(t, taskID),
		"a refused apply-shift must not move the linked task's due date")
	assert.Equal(t, shiftFixtureDue, eventStartDay(t, evtID),
		"a refused apply-shift must not move the event")

	// A viewer can read the calendar, so the refusal changes shape: they
	// are told the role is the problem rather than that nothing is there.
	//
	// The role it names is editor, because editor is the floor a shift
	// opens at and the last leg of this test proves it. Naming owner would
	// send a viewer to ask for permission the operation never required, on
	// a calendar whose owner is somebody else — the caller does what the
	// refusal said and is refused again.
	addCalendarMemberWithRole(t, host, calID, outsider.Email, "viewer")

	status, body = doJSONStatus(t, http.MethodPost, shiftBase+"/propose-shift",
		outsider.AccessToken, map[string]any{"newStartAt": newStart.Unix()})
	requireDenied(t, status, body, http.StatusForbidden, "CALENDAR.CALENDAR.EDITOR_ROLE_REQUIRED",
		"propose-shift for a calendar viewer")

	status, body = doJSONStatus(t, http.MethodPost, shiftBase+"/apply-shift",
		outsider.AccessToken, map[string]any{
			"newStartAt":       newStart.Unix(),
			"confirmedTaskIds": []string{taskID},
		})
	requireDenied(t, status, body, http.StatusForbidden, "CALENDAR.CALENDAR.EDITOR_ROLE_REQUIRED",
		"apply-shift for a calendar viewer")

	assert.Equal(t, shiftFixtureDue, taskDueDay(t, taskID),
		"a viewer's refused apply-shift must not move the linked task's due date")

	// Granting exactly the role the refusal named, and nothing above it.
	// This is what makes the refusal actionable rather than merely
	// well-formed: the caller does what it said and the request goes
	// through.
	doJSON(t, http.MethodPatch,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+calID+
			"/members/"+outsider.UserPublicID,
		host.AccessToken, map[string]any{"role": "editor"}, nil)

	var proposal struct {
		SafeTasks []struct {
			TaskID string `json:"taskId"`
		} `json:"safeTasks"`
	}
	doJSON(t, http.MethodPost, shiftBase+"/propose-shift", outsider.AccessToken,
		map[string]any{"newStartAt": newStart.Unix()}, &proposal)
	require.Len(t, proposal.SafeTasks, 1, "the linked task must be proposed as safe to shift")
	assert.Equal(t, taskID, proposal.SafeTasks[0].TaskID)

	var applied struct {
		Ok           bool  `json:"ok"`
		ShiftedTasks int32 `json:"shiftedTasks"`
	}
	doJSON(t, http.MethodPost, shiftBase+"/apply-shift", outsider.AccessToken,
		map[string]any{
			"newStartAt":       newStart.Unix(),
			"confirmedTaskIds": []string{taskID},
		}, &applied)
	assert.True(t, applied.Ok, "an editor must be able to shift the event")

	assert.Equal(t, "2027-06-08", eventStartDay(t, evtID),
		"the event must move once the caller holds editor on its calendar")
	assert.Equal(t, "2027-06-08", taskDueDay(t, taskID),
		"the confirmed task must move by the same day delta")
}
