package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLensCRUD verifies a workspace member can create, list, get,
// update, and delete saved views (lenses).
func TestLensCRUD(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"

	// --- Create a lens ---
	var created struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		IsDefault bool   `json:"isDefault"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"name":      "High Priority",
		"filter":    json.RawMessage(`{"priority":{"gte":3}}`),
		"sort":      json.RawMessage(`[]`),
		"isDefault": false,
	}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "High Priority", created.Name)

	// --- List includes the lens ---
	var list struct {
		Total  int64 `json:"total"`
		Lenses []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"lenses"`
	}
	doJSON(t, http.MethodGet, base, tt.AccessToken, nil, &list)
	require.GreaterOrEqual(t, list.Total, int64(1))

	// --- Get by ID ---
	var got struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	doJSON(t, http.MethodGet, base+"/"+created.ID, tt.AccessToken, nil, &got)
	require.Equal(t, created.ID, got.ID)

	// --- Update name ---
	var patched struct {
		Name string `json:"name"`
	}
	doJSON(t, http.MethodPatch, base+"/"+created.ID, tt.AccessToken,
		map[string]any{"name": "Critical Only"}, &patched)
	require.Equal(t, "Critical Only", patched.Name)

	// --- Delete ---
	status, _ := doJSONStatus(t, http.MethodDelete, base+"/"+created.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	// Deleted lens is gone from list.
	var listAfter struct {
		Total  int64 `json:"total"`
		Lenses []struct {
			ID string `json:"id"`
		} `json:"lenses"`
	}
	doJSON(t, http.MethodGet, base, tt.AccessToken, nil, &listAfter)
	for _, l := range listAfter.Lenses {
		require.NotEqual(t, created.ID, l.ID, "deleted lens must not appear")
	}
}

// TestLensPublishUnpublish verifies that a lens can be published for
// unauthenticated access and then unpublished to revoke it. The public
// payload also surfaces the lens description and the resolved task list
// projection (capped server-side) so anonymous viewers see real rows.
func TestLensPublishUnpublish(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"

	// Create a lens with a description so the public endpoint exposes it.
	var created struct {
		ID          string  `json:"id"`
		Description *string `json:"description"`
	}
	const lensDescription = "What we are working on this week."
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"name":        "Public Board",
		"description": lensDescription,
		"filter":      json.RawMessage(`{}`),
		"sort":        json.RawMessage(`[]`),
		"isDefault":   false,
	}, &created)
	require.NotNil(t, created.Description)
	require.Equal(t, lensDescription, *created.Description)

	// Public lens shares must expose only public-visibility task rows.
	var publicTask, projectTask, privateTask struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":  tt.ProjectPublicID,
		"title":      "public lens visible",
		"visibility": "public",
	}, &publicTask)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":  tt.ProjectPublicID,
		"title":      "public lens project hidden",
		"visibility": "project",
	}, &projectTask)
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId":  tt.ProjectPublicID,
		"title":      "public lens private hidden",
		"visibility": "private",
	}, &privateTask)
	require.NotEmpty(t, publicTask.ID)
	require.NotEmpty(t, projectTask.ID)
	require.NotEmpty(t, privateTask.ID)

	// Publish the lens.
	var published struct {
		PublicToken string `json:"publicToken"`
	}
	doJSON(t, http.MethodPost, base+"/"+created.ID+"/publish",
		tt.AccessToken, nil, &published)
	require.NotEmpty(t, published.PublicToken, "publish must return a token")

	// Public endpoint works without authentication and exposes the
	// description plus a (possibly empty) tasks array. The tasks slice
	// must always be non-null so the frontend can map over it without a
	// nil check.
	var pub struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Description *string `json:"description"`
		Tasks       []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"tasks"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/public/lenses/"+published.PublicToken,
		"", nil, &pub) // no bearer
	require.Equal(t, "Public Board", pub.Name)
	require.NotNil(t, pub.Description, "public payload must include the description")
	require.Equal(t, lensDescription, *pub.Description)
	require.NotNil(t, pub.Tasks, "tasks must be present even when empty")
	seenTasks := map[string]string{}
	for _, task := range pub.Tasks {
		seenTasks[task.ID] = task.Title
	}
	require.Equal(t, "public lens visible", seenTasks[publicTask.ID],
		"public task must be visible through public lens")
	require.NotContains(t, seenTasks, projectTask.ID,
		"project-visibility task must not leak through public lens")
	require.NotContains(t, seenTasks, privateTask.ID,
		"private task must not leak through public lens")

	// Unpublish revokes access.
	doJSON(t, http.MethodPost, base+"/"+created.ID+"/unpublish",
		tt.AccessToken, nil, nil)

	// Public endpoint should now fail.
	status, _ := doJSONStatus(t, http.MethodGet,
		testServerURL+"/public/lenses/"+published.PublicToken, "", nil)
	require.GreaterOrEqual(t, status, 400, "unpublished lens must not be accessible")
}
