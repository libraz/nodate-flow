package e2e

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSubtaskCreateAndListFilters covers the parentTaskId create field and
// the parentTaskId / topLevel list filters.
func TestSubtaskCreateAndListFilters(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost, testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	create := func(body map[string]any) string {
		t.Helper()
		var created struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, body, &created)
		require.NotEmpty(t, created.ID)
		return created.ID
	}
	parent := create(map[string]any{"projectId": owner.ProjectPublicID, "title": "parent " + randomHex(4)})
	other := create(map[string]any{"projectId": owner.ProjectPublicID, "title": "other " + randomHex(4)})
	childA := create(map[string]any{"projectId": owner.ProjectPublicID, "title": "child a", "parentTaskId": parent})
	childB := create(map[string]any{"projectId": owner.ProjectPublicID, "title": "child b", "parentTaskId": parent})

	list := func(token string, q url.Values) (int, []byte, taskListPage) {
		t.Helper()
		q.Set("workspaceId", owner.WorkspacePublicID)
		status, body := doJSONStatus(t, http.MethodGet, testServerURL+"/tasks?"+q.Encode(), token, nil)
		var page taskListPage
		if status == http.StatusOK {
			doJSON(t, http.MethodGet, testServerURL+"/tasks?"+q.Encode(), token, nil, &page)
		}
		return status, body, page
	}
	ids := func(p taskListPage) map[string]bool {
		out := map[string]bool{}
		for _, r := range p.Tasks {
			out[r.ID] = true
		}
		return out
	}

	_, _, children := list(owner.AccessToken, url.Values{"parentTaskId": {parent}})
	require.EqualValues(t, 2, children.Total)
	require.Equal(t, map[string]bool{childA: true, childB: true}, ids(children))

	_, _, top := list(owner.AccessToken, url.Values{"topLevel": {"true"}})
	require.True(t, ids(top)[parent])
	require.True(t, ids(top)[other])
	require.False(t, ids(top)[childA], "a subtask is not top level")

	status, body, _ := list(owner.AccessToken, url.Values{"parentTaskId": {parent}, "topLevel": {"true"}})
	requireDenied(t, status, body, http.StatusUnprocessableEntity, "VALIDATION.QUERY.FIELD_INVALID", "parentTaskId with topLevel")

	// Another project's task cannot be a parent.
	var proj struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/projects", owner.AccessToken,
		map[string]any{"slug": "sub-" + randomHex(4), "name": "Second"}, &proj)
	status, body = doJSONStatus(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{"projectId": proj.ID, "title": "cross", "parentTaskId": parent})
	requireDenied(t, status, body, http.StatusUnprocessableEntity, "WS.TASK.PARENT_PROJECT_MISMATCH", "cross-project parent")

	// Re-parenting: set, clear, self, cycle, cross-project.
	patch := func(id string, body map[string]any) (int, []byte) {
		t.Helper()
		return doJSONStatus(t, http.MethodPatch, testServerURL+"/tasks/"+id, owner.AccessToken, body)
	}
	status, body = patch(other, map[string]any{"parentTaskId": parent})
	require.Equal(t, http.StatusOK, status, string(body))
	_, _, children = list(owner.AccessToken, url.Values{"parentTaskId": {parent}})
	require.EqualValues(t, 3, children.Total)
	require.True(t, ids(children)[other])

	status, body = patch(other, map[string]any{"parentTaskId": ""})
	require.Equal(t, http.StatusOK, status, string(body))
	_, _, children = list(owner.AccessToken, url.Values{"parentTaskId": {parent}})
	require.EqualValues(t, 2, children.Total)

	status, body = patch(parent, map[string]any{"parentTaskId": parent})
	requireDenied(t, status, body, http.StatusUnprocessableEntity, "WS.TASK.PARENT_CYCLE", "self parent")
	status, body = patch(parent, map[string]any{"parentTaskId": childA})
	requireDenied(t, status, body, http.StatusUnprocessableEntity, "WS.TASK.PARENT_CYCLE", "descendant as parent")
	grandchild := create(map[string]any{"projectId": owner.ProjectPublicID, "title": "grandchild", "parentTaskId": childA})
	status, body = patch(parent, map[string]any{"parentTaskId": grandchild})
	requireDenied(t, status, body, http.StatusUnprocessableEntity, "WS.TASK.PARENT_CYCLE", "grandchild as parent")

	foreign := create(map[string]any{"projectId": proj.ID, "title": "foreign"})
	status, body = patch(foreign, map[string]any{"parentTaskId": parent})
	requireDenied(t, status, body, http.StatusUnprocessableEntity, "WS.TASK.PARENT_PROJECT_MISMATCH", "cross-project re-parent")

	// A private parent the member cannot see is answered as unknown, on both surfaces.
	hidden := create(map[string]any{"projectId": owner.ProjectPublicID, "title": "hidden", "visibility": "private"})
	status, body, _ = list(member.AccessToken, url.Values{"parentTaskId": {hidden}})
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND", "list under an invisible parent")
	status, body = doJSONStatus(t, http.MethodPost, testServerURL+"/tasks", member.AccessToken,
		map[string]any{"projectId": owner.ProjectPublicID, "title": "sneak", "parentTaskId": hidden})
	require.NotEqual(t, http.StatusCreated, status, string(body))
}
