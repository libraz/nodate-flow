// Authorization suite for removing a task↔event link.
//
// DELETE /tasks/{id}/links/{linkId} names two rows, and only one of them
// used to be checked. A link resolved by workspace and link id alone is
// reachable from any task's path, so the project-editor floor the route
// applies to {id} guards a task the request then never touches: the
// caller passes on a task they may write and the row that disappears
// hangs off a different one, possibly one they may not even read.
//
// The link id is unguessable and the refusal is a not-found either way,
// so nothing here is an existence oracle. What was missing is the
// binding.
package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// linkURL builds the delete route for one link under one task's path.
func linkURL(taskID, linkID string) string {
	return testServerURL + "/tasks/" + taskID + "/links/" + linkID
}

// createLinkVia creates a contributes_to link through the API as the
// given tenant and returns the link's public id. The link id is the
// whole subject of these tests, so it comes back from the route that
// mints it rather than being read out of the table.
func createLinkVia(t *testing.T, tt *helpers.TestTenant, taskID, evtID string) string {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/links", tt.AccessToken,
		map[string]any{"eventId": evtID, "relation": "contributes_to"}, &created)
	require.NotEmpty(t, created.ID, "create link must return a public id")
	return created.ID
}

// linkIsLive reports whether a link row is still enabled, read straight
// from the table so a refusal that answered 404 while deleting anyway
// cannot pass.
func linkIsLive(t *testing.T, linkID string) bool {
	t.Helper()
	var live bool
	require.NoError(t, testDB.QueryRow(
		`SELECT enabled FROM task_event_links WHERE public_id = UUID_TO_BIN(?, 0)`,
		linkID).Scan(&live))
	return live
}

// TestDeleteTaskEventLinkIsBoundToThePathTask walks the three cases the
// binding decides.
//
// The private task is the sharp one: its link is removable through its
// own path by someone who may read it, and the actor here may not. A
// route that resolves the link by workspace and id alone lets them
// delete it anyway, through a task of their own, having satisfied every
// check the route makes.
//
// The third leg is the counterweight: the same actor removes a link on a
// task they may write, on a calendar they hold no membership on. A
// regression that closed this by refusing every unlink cannot pass.
func TestDeleteTaskEventLinkIsBoundToThePathTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	editor := seedProjectRoleMember(t, host, "editor")

	calID := createCalendarMut(t, host, "Link Delete ACL Cal")
	evtID := createEventMut(t, host, calID, "Compensation review")

	// The host owns the calendar, so the host mints every link; the
	// editor deliberately never gets a grant on it.
	hiddenTask := createTaskWithVisibility(t, host, "Draft the banding proposal", "private")
	hiddenLink := createLinkVia(t, host, hiddenTask, evtID)

	sharedTask := createTaskWithVisibility(t, host, "Book the review slot", "public")
	sharedLink := createLinkVia(t, host, sharedTask, evtID)

	editableTask := createTaskWithVisibility(t, host, "Collect the manager forms", "public")

	// The editor cannot read the private task at all, which is what the
	// path binding has to hold on to: their own task's path must not
	// carry another task's link.
	status, body := doJSONStatus(t, http.MethodDelete, linkURL(editableTask, hiddenLink),
		editor.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "CALENDAR.EVENT.NOT_FOUND",
		"unlink of a link on a task the caller may not read, through a task they may write")
	assert.True(t, linkIsLive(t, hiddenLink),
		"a refused unlink must leave the link on the table")

	// Readability is not what makes the mismatch wrong. The same refusal
	// is due when the link's task is one the caller can read perfectly
	// well but did not name in the path.
	status, body = doJSONStatus(t, http.MethodDelete, linkURL(editableTask, sharedLink),
		editor.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "CALENDAR.EVENT.NOT_FOUND",
		"unlink of another readable task's link through the wrong path")
	assert.True(t, linkIsLive(t, sharedLink),
		"a link named under the wrong task must survive")

	// Through its own path the same call succeeds, and it succeeds
	// without any grant on the calendar the event lives on: the floor
	// this route takes is the task's, because an unlink returns nothing
	// about the event and the relation is the task's half as much as the
	// calendar's.
	var removed struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodDelete, linkURL(sharedTask, sharedLink), editor.AccessToken, nil, &removed)
	assert.True(t, removed.Ok, "a task editor must be able to unlink their own task's link")
	assert.False(t, linkIsLive(t, sharedLink), "a legitimate unlink must disable the row")
}

// TestDeleteTaskEventLinkRefusalMatchesAnUnknownLink states the shape of
// the refusal rather than trusting the codes to stay aligned: a link that
// belongs to another task must be answered exactly as an id naming
// nothing is.
//
// This is not an existence-oracle argument — a link id is a UUID nobody
// can guess. It is that the two cases have no reason to be told apart,
// and a route that starts telling them apart has started reporting on
// rows outside the task in the path.
func TestDeleteTaskEventLinkRefusalMatchesAnUnknownLink(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)

	calID := createCalendarMut(t, host, "Link Delete Refusal Cal")
	evtID := createEventMut(t, host, calID, "Vendor renegotiation")

	otherTask := createTaskWithVisibility(t, host, "Summarise the current terms", "public")
	otherLink := createLinkVia(t, host, otherTask, evtID)
	pathTask := createTaskWithVisibility(t, host, "Draft the counter-offer", "public")

	mismatchStatus, mismatchBody := doJSONStatus(t, http.MethodDelete,
		linkURL(pathTask, otherLink), host.AccessToken, nil)
	unknownStatus, unknownBody := doJSONStatus(t, http.MethodDelete,
		linkURL(pathTask, freshUUID(t)), host.AccessToken, nil)

	require.Equal(t, unknownStatus, mismatchStatus,
		"a link on another task must carry the status of one that does not exist")
	require.JSONEq(t, string(unknownBody), string(mismatchBody),
		"the two refusals must be the same response; body=%s", string(mismatchBody))
	requireDenied(t, mismatchStatus, mismatchBody, http.StatusNotFound,
		"CALENDAR.EVENT.NOT_FOUND", "unlink of a link belonging to another task")

	assert.True(t, linkIsLive(t, otherLink),
		"the other task's link must survive both refusals")
}
