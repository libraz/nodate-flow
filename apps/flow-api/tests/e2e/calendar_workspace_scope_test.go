package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCalendarRoutesAreScopedToTheWorkspaceInThePath pins the answer a
// client gets when {calId} does not live in {wsId}, and when the caller
// holds no grant on the calendar at all.
//
// The decision is made twice — once by the calendar-member middleware and
// again by the handler's own resolve — so this test cannot tell which layer
// produced it, and would still pass if either were removed. That is the
// point: it states the contract rather than the implementation, so the
// duplicate can be collapsed without the assertion moving.
func TestCalendarRoutesAreScopedToTheWorkspaceInThePath(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	host := newTenant(t)
	calID := createCalendarMut(t, host, "Workspace Scope Cal")

	// A second workspace the same user owns. The membership row on the
	// calendar is unchanged; only the workspace named in the path differs.
	otherWorkspace := newTenant(t)

	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+otherWorkspace.WorkspacePublicID+"/calendars/"+calID,
		host.AccessToken, nil)
	require.NotEqualf(t, http.StatusOK, status,
		"a calendar reached through a workspace it does not belong to must not resolve; body=%s",
		string(body))

	// Same request through the calendar's own workspace still works, so a
	// regression that denies everything cannot pass this test.
	var fetched struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+calID,
		host.AccessToken, nil, &fetched)
	require.Equal(t, calID, fetched.ID)

	// And a workspace member who was never granted the calendar is refused
	// by the gate itself, not by anything downstream of it.
	outsider := inviteAndJoinWorkspace(t, host)
	status, body = doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+host.WorkspacePublicID+"/calendars/"+calID,
		outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "CALENDAR.CALENDAR.ACCESS_DENIED",
		"a workspace member with no calendar_members row reading a calendar")
}
