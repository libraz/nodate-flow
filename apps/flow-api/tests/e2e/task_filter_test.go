package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTaskFilterByState verifies that the task list can be
// filtered by derivedState query parameter.
func TestTaskFilterByState(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Create two tasks.
	var openTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Open task"}, &openTask)

	var waitingTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Waiting task"}, &waitingTask)

	// Transition second task to "waiting".
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+waitingTask.ID+"/transitions",
		tt.AccessToken, map[string]any{"transition": "start"}, nil)

	// Filter by state=open — should only see the open task.
	var openList struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?projectId="+tt.ProjectPublicID+"&state=open",
		tt.AccessToken, nil, &openList)

	foundOpen := false
	foundWaiting := false
	for _, task := range openList.Tasks {
		if task.ID == openTask.ID {
			foundOpen = true
		}
		if task.ID == waitingTask.ID {
			foundWaiting = true
		}
	}
	require.True(t, foundOpen, "open task must appear in state=open filter")
	require.False(t, foundWaiting, "waiting task must not appear in state=open filter")
}

// TestTaskFilterByAssignee verifies that the task list can be
// filtered by assignee user ID.
func TestTaskFilterByAssignee(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	// Create two tasks in the same project.
	var ownerTask, memberTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{"projectId": owner.ProjectPublicID, "title": "Owner task"}, &ownerTask)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{"projectId": owner.ProjectPublicID, "title": "Member task"}, &memberTask)

	// Assign owner to ownerTask, member to memberTask.
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+ownerTask.ID+"/actors",
		owner.AccessToken, map[string]any{"userId": owner.UserPublicID, "role": "assignee"}, nil)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+memberTask.ID+"/actors",
		owner.AccessToken, map[string]any{"userId": member.UserPublicID, "role": "assignee"}, nil)

	// Filter by assignee=member — should only see memberTask.
	var filtered struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?projectId="+owner.ProjectPublicID+"&assignee="+member.UserPublicID,
		owner.AccessToken, nil, &filtered)

	foundOwner := false
	foundMember := false
	for _, task := range filtered.Tasks {
		if task.ID == ownerTask.ID {
			foundOwner = true
		}
		if task.ID == memberTask.ID {
			foundMember = true
		}
	}
	require.True(t, foundMember, "assigned task must appear in assignee filter")
	require.False(t, foundOwner, "unassigned task must not appear in assignee filter")
}

// TestTaskListPagination verifies that limit and offset work
// correctly in task listing.
func TestTaskListPagination(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Create 3 tasks.
	for i := 0; i < 3; i++ {
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
			map[string]any{
				"projectId": tt.ProjectPublicID,
				"title":     "Paginate " + randomHex(4),
			}, nil)
	}

	// Page 1: limit=2, offset=0.
	var page1 struct {
		Total int64 `json:"total"`
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?projectId="+tt.ProjectPublicID+"&limit=2&offset=0",
		tt.AccessToken, nil, &page1)
	require.GreaterOrEqual(t, page1.Total, int64(3))
	require.Len(t, page1.Tasks, 2)

	// Page 2: limit=2, offset=2.
	var page2 struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?projectId="+tt.ProjectPublicID+"&limit=2&offset=2",
		tt.AccessToken, nil, &page2)
	require.GreaterOrEqual(t, len(page2.Tasks), 1)

	// No overlap between pages.
	page1IDs := map[string]bool{}
	for _, task := range page1.Tasks {
		page1IDs[task.ID] = true
	}
	for _, task := range page2.Tasks {
		require.False(t, page1IDs[task.ID],
			"page 2 must not overlap with page 1")
	}
}

// TestTaskListByWorkspace verifies that listing tasks by
// workspaceId returns tasks across all projects in that workspace.
func TestTaskListByWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Create a second project in the same workspace.
	var proj2 struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/projects",
		tt.AccessToken, map[string]any{
			"name": "Second Project " + randomHex(4),
			"slug": "proj2-" + randomHex(4),
		}, &proj2)

	// Create a task in each project.
	var task1, task2 struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Project 1 task"}, &task1)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": proj2.ID, "title": "Project 2 task"}, &task2)

	// List by workspaceId — both tasks should appear.
	var list struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks?workspaceId="+tt.WorkspacePublicID,
		tt.AccessToken, nil, &list)

	found1 := false
	found2 := false
	for _, task := range list.Tasks {
		if task.ID == task1.ID {
			found1 = true
		}
		if task.ID == task2.ID {
			found2 = true
		}
	}
	require.True(t, found1, "task from project 1 must appear")
	require.True(t, found2, "task from project 2 must appear")
}
