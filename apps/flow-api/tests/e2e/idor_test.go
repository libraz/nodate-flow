package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIDORTaskCrossWorkspace verifies that knowing a task's public UUID
// from another workspace does not grant access.
func TestIDORTaskCrossWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	// Owner creates a task.
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{"projectId": owner.ProjectPublicID, "title": "Confidential"}, &task)

	// Outsider knows the task UUID but is in a different workspace.
	status, _ := doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403,
		"outsider must not access task from another workspace")
}

// TestIDORTaskUpdate verifies that an outsider cannot update a task
// from another workspace even with a known UUID.
func TestIDORTaskUpdate(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{"projectId": owner.ProjectPublicID, "title": "Secret Task"}, &task)

	// Outsider tries to update the task.
	status, _ := doJSONStatus(t, http.MethodPatch,
		testServerURL+"/tasks/"+task.ID, outsider.AccessToken,
		map[string]any{"title": "Hacked Title"})
	require.GreaterOrEqual(t, status, 403,
		"outsider must not update task from another workspace")

	// Verify the title was NOT changed.
	var got struct {
		Title string `json:"title"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/tasks/"+task.ID,
		owner.AccessToken, nil, &got)
	require.Equal(t, "Secret Task", got.Title,
		"task title must remain unchanged after failed IDOR attempt")
}

// TestIDORTaskDelete verifies that an outsider cannot delete a task
// from another workspace.
func TestIDORTaskDelete(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{"projectId": owner.ProjectPublicID, "title": "Protected"}, &task)

	// Outsider tries to delete.
	status, _ := doJSONStatus(t, http.MethodDelete,
		testServerURL+"/tasks/"+task.ID, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403,
		"outsider must not delete task from another workspace")

	// Owner can still access it.
	status, _ = doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID, owner.AccessToken, nil)
	require.Equal(t, http.StatusOK, status,
		"task must still exist after failed IDOR delete")
}

// TestIDORProjectCrossWorkspace verifies that knowing a project's UUID
// from another workspace does not grant access.
func TestIDORProjectCrossWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	// Outsider tries to access owner's project.
	status, _ := doJSONStatus(t, http.MethodGet,
		testServerURL+"/projects/"+owner.ProjectPublicID, outsider.AccessToken, nil)
	require.GreaterOrEqual(t, status, 403,
		"outsider must not access project from another workspace")
}

// TestNonexistentUUIDReturns404 verifies that requesting a resource
// with a valid UUID format but nonexistent ID returns 404 (not 500).
func TestNonexistentUUIDReturns404(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	fakeUUID := "019fffff-ffff-7fff-bfff-ffffffffffff"

	endpoints := []struct {
		name   string
		method string
		path   string
	}{
		{"task", http.MethodGet, testServerURL + "/tasks/" + fakeUUID},
		{"project", http.MethodGet, testServerURL + "/projects/" + fakeUUID},
		{"page", http.MethodGet, testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/pages/" + fakeUUID},
		{"widget", http.MethodGet, testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/dashboard/widgets/" + fakeUUID},
		{"lens", http.MethodGet, testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses/" + fakeUUID},
		{"timebox", http.MethodGet, testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/timeboxes/" + fakeUUID},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			status, _ := doJSONStatus(t, ep.method, ep.path, tt.AccessToken, nil)
			require.Equal(t, http.StatusNotFound, status,
				"nonexistent %s must return 404, not 500", ep.name)
		})
	}
}
