package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// Reversing an event is a write to the task the event belongs to: the
// compensating row lands on that task's timeline and, for the event
// types in the rollback table, walks its derived_state back. Workspace
// membership alone is therefore the wrong gate — it lets any member undo
// agent activity on a task they are not allowed to read.
//
// The pair below is deliberate. A test that only asserts the denial
// passes just as well against a handler that refuses every reversal, so
// the same member drives a task they can see and must still get 201.

// reverseStatusAs issues POST .../reverse against a workspace the
// caller does not own. The shared [reverseStatus] helper takes the
// workspace from the same tenant it takes the token from, which is
// exactly wrong here: the whole point is a second user acting inside
// somebody else's workspace.
func reverseStatusAs(t *testing.T, workspacePublicID string, as *helpers.TestTenant, eventPublicID string) (int, []byte) {
	t.Helper()
	return doJSONStatus(t,
		http.MethodPost,
		testServerURL+"/workspaces/"+workspacePublicID+"/events/"+eventPublicID+"/reverse",
		as.AccessToken,
		nil,
	)
}

// seedWorkspaceMemberTenant invites a second tenant into owner's
// workspace with the plain member role — not guest, which the group's
// write floor rejects before visibility is ever consulted.
func seedWorkspaceMemberTenant(t *testing.T, owner *helpers.TestTenant) *helpers.TestTenant {
	t.Helper()
	member := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	require.NotEmpty(t, invite.Token, "invite create returned no token")
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	return member
}

// createTaskWithVisibility creates a task in the tenant's default
// project with an explicit visibility.
func createTaskWithVisibility(t *testing.T, tt *helpers.TestTenant, title, visibility string) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":  tt.ProjectPublicID,
		"title":      title,
		"visibility": visibility,
	}, &out)
	require.NotEmpty(t, out.ID, "task create returned empty id")
	return out.ID
}

// TestReverseRejectsEventOnInvisibleTask drives the denial: a workspace
// member who is neither the creator nor an actor on a private task must
// not be able to reverse an agent event attached to it, and must not
// learn from the response that the event exists.
func TestReverseRejectsEventOnInvisibleTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberTenant(t, owner)

	taskID := createTaskWithVisibility(t, owner, "Reverse: private task", "private")
	agent := helpers.SeedAgent(t, testDB, owner.WorkspacePublicID, helpers.SeedAgentOptions{})
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)

	_, origPub := appendAgentEventForTask(t, testDB, wsID, taskInternalID, agent.AgentID,
		"ai.agent.run.completed")

	// Sanity: the member genuinely cannot read the task, so the reversal
	// result below is about visibility and not about a mis-seeded fixture.
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+taskID, member.AccessToken, nil)
	require.NotEqual(t, http.StatusOK, status,
		"fixture: the member must not be able to read the private task; body=%s", string(body))

	status, body = reverseStatusAs(t, owner.WorkspacePublicID, member, origPub)
	require.Equal(t, http.StatusNotFound, status,
		"reversing an event on an unreadable task must be refused; body=%s", string(body))
	require.Contains(t, string(body), "AI.REVERSE.TARGET_NOT_FOUND",
		"denial must not distinguish itself from a miss; body=%s", string(body))

	// Nothing may have been written on the way to the refusal.
	var reverseCount int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND task_id = ? AND reverses_event_id IS NOT NULL`,
		wsID, taskInternalID,
	).Scan(&reverseCount))
	require.Zero(t, reverseCount, "a refused reversal must append no compensating event")
}

// TestReverseAllowsEventOnVisibleTask is the other half: the same
// non-admin member reversing an event on a task they can read still
// gets the 201. Without it, a handler that denied everything would
// satisfy the test above.
func TestReverseAllowsEventOnVisibleTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberTenant(t, owner)

	taskID := createTaskWithVisibility(t, owner, "Reverse: public task", "public")
	agent := helpers.SeedAgent(t, testDB, owner.WorkspacePublicID, helpers.SeedAgentOptions{})
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)

	origID, origPub := appendAgentEventForTask(t, testDB, wsID, taskInternalID, agent.AgentID,
		"ai.agent.run.completed")

	status, body := reverseStatusAs(t, owner.WorkspacePublicID, member, origPub)
	require.Equal(t, http.StatusCreated, status,
		"a member who can read the task must still be able to reverse; body=%s", string(body))

	row := selectReverseRow(t, testDB, wsID, origID)
	require.Equal(t, "ai.agent.run.completed", row.Type,
		"compensating event must reuse the original type")
}

// TestReverseAllowsAdminOnPrivateTask pins the elevation half of the
// visibility rule: a workspace admin reads every task, so the same
// private-task event the member was refused stays reversible for the
// owner. This is the assertion that fails if the visibility check is
// ever tightened into a blanket creator-only rule.
func TestReverseAllowsAdminOnPrivateTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	taskID := createTaskWithVisibility(t, owner, "Reverse: private, admin actor", "private")
	agent := helpers.SeedAgent(t, testDB, owner.WorkspacePublicID, helpers.SeedAgentOptions{})
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)

	_, origPub := appendAgentEventForTask(t, testDB, wsID, taskInternalID, agent.AgentID,
		"ai.agent.run.completed")

	status, body := reverseStatus(t, owner, origPub)
	require.Equal(t, http.StatusCreated, status,
		"the workspace owner must keep reversing events on private tasks; body=%s", string(body))
}
