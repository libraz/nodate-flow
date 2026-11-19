package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskDueDateFilter(t *testing.T) {
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

	tasks := []struct {
		name       string
		dueOn      string
		visibility string
	}{
		{name: "before", dueOn: "2035-01-09"},
		{name: "from", dueOn: "2035-01-10"},
		{name: "inside", dueOn: "2035-01-11"},
		{name: "through", dueOn: "2035-01-12"},
		{name: "after", dueOn: "2035-01-13"},
		{name: "undated"},
		{name: "private", dueOn: "2035-01-11", visibility: "private"},
	}
	ids := make(map[string]string, len(tasks))
	for _, task := range tasks {
		body := map[string]any{
			"projectId": owner.ProjectPublicID,
			"title":     "due filter " + task.name + " " + randomHex(4),
		}
		if task.dueOn != "" {
			body["dueOn"] = task.dueOn
		}
		if task.visibility != "" {
			body["visibility"] = task.visibility
		}
		var created struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, body, &created)
		require.NotEmpty(t, created.ID)
		ids[task.name] = created.ID
	}

	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+ids["inside"]+"/transitions",
		owner.AccessToken, map[string]any{"transition": "complete"}, nil)

	query := url.Values{
		"workspaceId": {owner.WorkspacePublicID},
		"dueFrom":     {"2035-01-10"},
		"dueThrough":  {"2035-01-12"},
		"limit":       {"2"},
	}
	fetch := func(token string, offset int) taskListPage {
		t.Helper()
		query.Set("offset", fmt.Sprint(offset))
		var page taskListPage
		doJSON(t, http.MethodGet, testServerURL+"/tasks?"+query.Encode(), token, nil, &page)
		return page
	}

	first := fetch(owner.AccessToken, 0)
	require.EqualValues(t, 4, first.Total)
	require.Len(t, first.Tasks, 2)
	second := fetch(owner.AccessToken, 2)
	require.EqualValues(t, 4, second.Total)
	require.Len(t, second.Tasks, 2)
	farOffset := fetch(owner.AccessToken, 50)
	require.EqualValues(t, 4, farOffset.Total, "an empty page beyond the end still reports the filtered total")
	require.Empty(t, farOffset.Tasks)

	seen := map[string]bool{}
	for _, row := range append(first.Tasks, second.Tasks...) {
		seen[row.ID] = true
	}
	require.Equal(t, map[string]bool{
		ids["from"]:    true,
		ids["inside"]:  true,
		ids["through"]: true,
		ids["private"]: true,
	}, seen, "both range endpoints are inclusive, and dates outside the range and undated tasks are excluded")

	query.Set("state", "open")
	query.Set("offset", "0")
	var openPage taskListPage
	doJSON(t, http.MethodGet, testServerURL+"/tasks?"+query.Encode(), owner.AccessToken, nil, &openPage)
	require.EqualValues(t, 3, openPage.Total, "state filtering composes with due date filtering")
	for _, row := range openPage.Tasks {
		require.NotEqual(t, ids["inside"], row.ID, "a terminal task is not returned when the bot requests open tasks")
	}

	pat := mintPatForWorkspace(t, owner.AccessToken, owner.UserPublicID, owner.WorkspacePublicID)
	projectQuery := url.Values{
		"projectId":  {owner.ProjectPublicID},
		"dueFrom":    {"2035-01-10"},
		"dueThrough": {"2035-01-12"},
		"limit":      {"2"},
	}
	botPage := fetchTaskListPage(t, pat, projectQuery.Encode())
	require.EqualValues(t, 4, botPage.Total, "a workspace-bound bot token can use the project-scoped due filter")

	query.Del("state")
	memberPage := fetch(member.AccessToken, 0)
	require.EqualValues(t, 3, memberPage.Total, "invisible private tasks must not leak through total")
	for _, row := range memberPage.Tasks {
		require.NotEqual(t, ids["private"], row.ID)
	}

	query.Set("cursor", "eyJ0ZXN0IjoiY3Vyc29yIn0")
	status, body := doJSONStatus(t, http.MethodGet, testServerURL+"/tasks?"+query.Encode(), owner.AccessToken, nil)
	require.Equal(t, http.StatusUnprocessableEntity, status, string(body))
	require.Equal(t, "VALIDATION.QUERY.FIELD_INVALID", problemType(t, body))

	query.Del("cursor")
	query.Set("dueFrom", "2035-02-30")
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks?"+query.Encode(), owner.AccessToken, nil)
	require.Equal(t, http.StatusUnprocessableEntity, status, string(body))
	require.Equal(t, "VALIDATION.QUERY.FIELD_INVALID", problemType(t, body))

	query.Set("dueFrom", "2035-01-13")
	query.Set("dueThrough", "2035-01-10")
	status, body = doJSONStatus(t, http.MethodGet, testServerURL+"/tasks?"+query.Encode(), owner.AccessToken, nil)
	require.Equal(t, http.StatusUnprocessableEntity, status, string(body))
	require.Equal(t, "VALIDATION.QUERY.FIELD_INVALID", problemType(t, body))
}
