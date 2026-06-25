package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestActorRemove verifies that an actor can be removed from a
// task after being added.
func TestActorRemove(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Create the task with an explicit empty-ish actor slot: pass an
	// explicit actors entry for the creator so we can grab its public
	// id on the response path and then exercise RemoveActor. We use
	// the explicit form to sidestep the auto-attach fallback while
	// still keeping only the creator on the task.
	var task struct {
		ID         string `json:"id"`
		ActorCount int    `json:"actorCount"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{
			"projectId": tt.ProjectPublicID,
			"title":     "Actor remove test",
			"actors": []map[string]any{
				{"userId": tt.UserPublicID, "role": "assignee"},
			},
		}, &task)

	// Look up the single actor row so we have a public id to delete.
	var list struct {
		Total  int64 `json:"total"`
		Actors []struct {
			ID     string `json:"id"`
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"actors"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID+"/actors",
		tt.AccessToken, nil, &list)
	require.Equal(t, int64(1), list.Total)
	require.Equal(t, tt.UserPublicID, list.Actors[0].UserID)

	// Remove the actor.
	var ok struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodDelete,
		testServerURL+"/tasks/"+task.ID+"/actors/"+list.Actors[0].ID,
		tt.AccessToken, nil, &ok)
	require.True(t, ok.Ok)

	// Verify actor count is 0.
	var detail struct {
		ActorCount int `json:"actorCount"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID,
		tt.AccessToken, nil, &detail)
	require.Equal(t, 0, detail.ActorCount)
}

// TestDependencyRemove verifies that a dependency edge can be
// removed from a task after being added.
func TestDependencyRemove(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task1, task2 struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Dep source"}, &task1)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Dep target"}, &task2)

	// Add dependency.
	var dep struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task1.ID+"/dependencies",
		tt.AccessToken, map[string]any{
			"toTaskId": task2.ID,
			"kind":     "blocks",
		}, &dep)
	require.NotEmpty(t, dep.ID)

	// Remove dependency.
	var ok struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodDelete,
		testServerURL+"/tasks/"+task1.ID+"/dependencies/"+dep.ID,
		tt.AccessToken, nil, &ok)
	require.True(t, ok.Ok)

	// Verify dependency count is 0.
	var detail struct {
		DependencyCount int `json:"dependencyCount"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+task1.ID,
		tt.AccessToken, nil, &detail)
	require.Equal(t, 0, detail.DependencyCount)
}

// TestConstraintRemove verifies that a constraint can be removed
// from a task.
func TestConstraintRemove(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": "Constraint remove"}, &task)

	// Add a constraint.
	var constraint struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/constraints",
		tt.AccessToken, map[string]any{
			"kind":       "deadline",
			"expression": `{"op":"time.due_before","arg":"2026-12-31"}`,
		}, &constraint)
	require.NotEmpty(t, constraint.ID)

	// Remove constraint.
	var ok struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodDelete,
		testServerURL+"/tasks/"+task.ID+"/constraints/"+constraint.ID,
		tt.AccessToken, nil, &ok)
	require.True(t, ok.Ok)

	// Verify constraint count is 0.
	var detail struct {
		ConstraintCount int `json:"constraintCount"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID,
		tt.AccessToken, nil, &detail)
	require.Equal(t, 0, detail.ConstraintCount)
}
