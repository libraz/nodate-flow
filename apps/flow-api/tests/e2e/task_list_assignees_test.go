package e2e

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

type listedAssignee struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

func listAssignees(t *testing.T, token, query string) map[string][]listedAssignee {
	t.Helper()
	var page struct {
		Tasks []struct {
			ID        string           `json:"id"`
			Assignees []listedAssignee `json:"assignees"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks?"+query, token, nil, &page)
	out := map[string][]listedAssignee{}
	for _, task := range page.Tasks {
		require.NotNil(t, task.Assignees, "assignees is always a list, never null (task %s)", task.ID)
		out[task.ID] = task.Assignees
	}
	return out
}

// TestTaskListCarriesAssigneeNames pins that list rows name their human
// assignees in assignment order and leave agents out.
func TestTaskListCarriesAssigneeNames(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := inviteMember(t, owner)
	solo := createTask(t, owner.AccessToken, owner.ProjectPublicID, "Creator only")
	shared := createTask(t, owner.AccessToken, owner.ProjectPublicID, "Creator then member")
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+shared+"/actors", owner.AccessToken,
		map[string]any{"userId": member.UserPublicID, "role": "assignee"}, nil)

	agent := helpers.SeedAgent(t, testDB, owner.WorkspacePublicID, helpers.SeedAgentOptions{}).AgentPublicID
	withAgent := createTask(t, owner.AccessToken, owner.ProjectPublicID, "Creator plus agent")
	status, body := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/"+withAgent+"/agents",
		owner.AccessToken, map[string]any{"agentId": agent, "role": "assignee"})
	require.Truef(t, status >= 200 && status < 300, "attach agent: %d %s", status, string(body))

	// Both list scopes must carry assignees: a project-scoped list and a
	// workspace-scoped one resolve the workspace differently.
	for name, query := range map[string]string{
		"project":   "projectId=" + url.QueryEscape(owner.ProjectPublicID),
		"workspace": "workspaceId=" + url.QueryEscape(owner.WorkspacePublicID),
	} {
		t.Run(name, func(t *testing.T) {
			requireAssigneeShapes(t, listAssignees(t, owner.AccessToken, query), owner, member, solo, shared, withAgent)
		})
	}
}

func requireAssigneeShapes(t *testing.T, got map[string][]listedAssignee, owner, member *helpers.TestTenant, solo, shared, withAgent string) {
	t.Helper()
	require.Equal(t, []listedAssignee{{ID: owner.UserPublicID, DisplayName: owner.DisplayName}}, got[solo])
	require.Equal(t, []listedAssignee{
		{ID: owner.UserPublicID, DisplayName: owner.DisplayName},
		{ID: member.UserPublicID, DisplayName: member.DisplayName},
	}, got[shared], "assignees follow assignment order")
	require.Equal(t, []listedAssignee{{ID: owner.UserPublicID, DisplayName: owner.DisplayName}},
		got[withAgent], "an agent assignee is not listed")
}

// TestTaskListAssigneesDropRemovedUser pins that a removed assignee
// leaves the list row.
func TestTaskListAssigneesDropRemovedUser(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := inviteMember(t, owner)
	task := createTask(t, owner.AccessToken, owner.ProjectPublicID, "Reassigned")
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task+"/actors", owner.AccessToken,
		map[string]any{"userId": member.UserPublicID, "role": "assignee"}, nil)
	actorID := visibleUserActorID(t, owner.AccessToken, task, member.UserPublicID)
	require.NotEmpty(t, actorID)

	query := "projectId=" + url.QueryEscape(owner.ProjectPublicID)
	require.Len(t, listAssignees(t, owner.AccessToken, query)[task], 2)

	removeTaskActorForMutationTest(t, owner.AccessToken, task, actorID)

	require.Equal(t, []listedAssignee{{ID: owner.UserPublicID, DisplayName: owner.DisplayName}},
		listAssignees(t, owner.AccessToken, query)[task])
}
