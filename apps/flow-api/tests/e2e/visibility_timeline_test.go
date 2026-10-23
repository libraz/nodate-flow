package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// workspaceTimelineBody returns the raw workspace timeline body for one
// reader. Raw rather than decoded, because the assertions are about a
// string never appearing in anything the endpoint returns.
func workspaceTimelineBody(t *testing.T, workspacePublicID string, as *helpers.TestTenant) string {
	t.Helper()
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+workspacePublicID+"/timeline?limit=200",
		as.AccessToken, nil)
	require.Equal(t, http.StatusOK, status, "workspace timeline must render; body=%s", string(body))
	return string(body)
}

// TestTimelineHidesEventsOfDeletedPrivateTask holds that deleting a
// private task does not turn its history into workspace-root events. The
// timeline view NULLs the task id of a disabled task, so a predicate keyed
// on that column read the old task.created payload — title included — as
// readable by every member.
func TestTimelineHidesEventsOfDeletedPrivateTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := seedGuestMember(t, owner)

	suffix := uuid.NewString()
	secretTitle := "Vis TL deleted private " + suffix
	openTitle := "Vis TL public " + suffix

	secretID := createTaskWithVisibility(t, owner, secretTitle, "private")
	createTaskWithVisibility(t, owner, openTitle, "public")

	status, body := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/tasks/"+secretID, owner.AccessToken, nil)
	require.Less(t, status, 300, "fixture: deleting the private task; body=%s", string(body))

	got := workspaceTimelineBody(t, owner.WorkspacePublicID, guest)
	assert.NotContains(t, got, secretTitle,
		"a deleted private task's events must not reach a guest who could never read it")
	assert.Contains(t, got, openTitle,
		"public task events must still reach the guest, or this test proves nothing")

	// The owner is the elevated reader and keeps the full trail.
	assert.Contains(t, workspaceTimelineBody(t, owner.WorkspacePublicID, owner), secretTitle,
		"workspace owner must still see the deleted task's history")
}

// TestTimelineHidesCalendarEventsTheReaderCannotRead holds calendar rows
// to the calendar endpoints' own rule: membership of the calendar, then
// the event row's visibility. Before, every member's timeline carried the
// title of a confidential event and of events on calendars they were not
// a member of.
func TestTimelineHidesCalendarEventsTheReaderCannotRead(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := seedGuestMember(t, owner)

	suffix := uuid.NewString()
	sharedCal := createCalendarMut(t, owner, "Vis TL shared "+suffix)
	addCalendarMemberWithRole(t, owner, sharedCal, guest.Email, "viewer")
	privateCal := createCalendarMut(t, owner, "Vis TL not shared "+suffix)

	start := time.Date(2027, 11, 10, 10, 0, 0, 0, time.UTC)
	confidentialTitle := "Vis TL confidential " + suffix
	nonMemberTitle := "Vis TL non-member " + suffix
	openTitle := "Vis TL open " + suffix

	createEventWithVisibility(t, owner, sharedCal, confidentialTitle, "confidential", start)
	createEventWithVisibility(t, owner, privateCal, nonMemberTitle, "default", start)
	createEventWithVisibility(t, owner, sharedCal, openTitle, "default", start.Add(time.Hour))

	got := workspaceTimelineBody(t, owner.WorkspacePublicID, guest)
	assert.NotContains(t, got, confidentialTitle,
		"a confidential event must not reach anyone but its owner through the timeline")
	assert.NotContains(t, got, nonMemberTitle,
		"an event on a calendar the reader is not a member of must not reach the timeline")
	assert.NotContains(t, got, privateCal,
		"the public id of a calendar the reader cannot open must not leak through payloads")
	assert.Contains(t, got, openTitle,
		"an ordinary event on a calendar the reader belongs to must still appear")
}
