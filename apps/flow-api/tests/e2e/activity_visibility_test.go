package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// The workspace activity feed unions audit_logs with the AI and MCP
// invocation trails. audit_logs has its own read endpoint sitting behind
// a workspace-admin gate, so the feed must not become the way around it:
// a member reading /activity may see what they did themselves and what
// happened to tasks they can open, and nothing else.
//
// The assertions below name specific resource ids rather than counting
// rows, because the suite runs against a shared instance.

// activityBody fetches the activity feed as the given tenant and returns
// the raw response body.
func activityBody(t *testing.T, workspacePublicID string, as *helpers.TestTenant) []byte {
	t.Helper()
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+workspacePublicID+"/activity?limit=200",
		as.AccessToken, nil)
	require.Equal(t, http.StatusOK, status,
		"activity feed must be readable by a workspace member; body=%s", string(body))
	return body
}

// TestActivityFeedHidesUnreadableTasksFromMembers drives the narrowing:
// a private task the member cannot open must not surface its id through
// the feed, while the member's own action and a task they can read still
// do.
func TestActivityFeedHidesUnreadableTasksFromMembers(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberTenant(t, owner)

	privateTaskID := createTaskWithVisibility(t, owner, "Activity: private", "private")
	publicTaskID := createTaskWithVisibility(t, owner, "Activity: public", "public")

	// The member's own action. A label is the write a plain member is
	// allowed to make without project membership, and it is audited.
	var label struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/labels",
		member.AccessToken,
		map[string]any{"name": "activity-vis-" + randomHex(6), "color": "#3366ff"}, &label)
	require.NotEmpty(t, label.ID, "fixture: the member's label create must succeed")

	// Sanity: the member genuinely cannot read the private task.
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+privateTaskID, member.AccessToken, nil)
	require.NotEqual(t, http.StatusOK, status,
		"fixture: the member must not be able to read the private task; body=%s", string(body))

	memberFeed := string(activityBody(t, owner.WorkspacePublicID, member))
	require.NotContains(t, memberFeed, privateTaskID,
		"the feed must not report activity on a task the member cannot open")
	require.Contains(t, memberFeed, publicTaskID,
		"the feed must still report activity on a task the member can open")
	require.Contains(t, memberFeed, label.ID,
		"the feed must still report the member's own activity")
}

// TestActivityFeedKeepsAdministrationTrailForAdminsOnly pins the other
// half: the workspace-administration entries a member has no business
// reading stay visible to the admin whose endpoint they belong to.
func TestActivityFeedKeepsAdministrationTrailForAdminsOnly(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberTenant(t, owner)

	// An owner-only write: a label the member never touched.
	var label struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/labels",
		owner.AccessToken,
		map[string]any{"name": "activity-admin-" + randomHex(6), "color": "#993366"}, &label)
	require.NotEmpty(t, label.ID, "fixture: the owner's label create must succeed")

	privateTaskID := createTaskWithVisibility(t, owner, "Activity: admin sees private", "private")

	memberFeed := string(activityBody(t, owner.WorkspacePublicID, member))
	require.NotContains(t, memberFeed, label.ID,
		"a member must not read the workspace administration trail through the feed")

	ownerFeed := string(activityBody(t, owner.WorkspacePublicID, owner))
	require.Contains(t, ownerFeed, label.ID,
		"the admin audience keeps the full trail")
	require.Contains(t, ownerFeed, privateTaskID,
		"the admin audience keeps activity on private tasks")
}
