package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPageCRUD verifies a workspace member can create, read, update,
// list, and soft-delete pages.
func TestPageCRUD(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	// --- Create a page ---
	var created struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	doJSON(t, http.MethodPost, wsURL+"/pages", tt.AccessToken,
		map[string]any{"title": "Getting Started", "body": "Welcome to the wiki."}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "Getting Started", created.Title)
	require.Equal(t, "Welcome to the wiki.", created.Body)

	// --- List pages should include the created page ---
	var list struct {
		Total int64 `json:"total"`
		Pages []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"pages"`
	}
	doJSON(t, http.MethodGet, wsURL+"/pages", tt.AccessToken, nil, &list)
	require.GreaterOrEqual(t, list.Total, int64(1))
	found := false
	for _, p := range list.Pages {
		if p.ID == created.ID {
			found = true
		}
	}
	require.True(t, found, "created page must appear in list")

	// --- Get by ID returns full content ---
	var got struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	doJSON(t, http.MethodGet, wsURL+"/pages/"+created.ID, tt.AccessToken, nil, &got)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, "Welcome to the wiki.", got.Body)

	// --- Update title and body ---
	var patched struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	doJSON(t, http.MethodPatch, wsURL+"/pages/"+created.ID, tt.AccessToken,
		map[string]any{"title": "Updated Title", "body": "New content."}, &patched)
	require.Equal(t, "Updated Title", patched.Title)
	require.Equal(t, "New content.", patched.Body)

	// --- Delete (soft) ---
	status, _ := doJSONStatus(t, http.MethodDelete, wsURL+"/pages/"+created.ID,
		tt.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)

	// --- Deleted page should not appear in list ---
	var listAfter struct {
		Total int64 `json:"total"`
		Pages []struct {
			ID string `json:"id"`
		} `json:"pages"`
	}
	doJSON(t, http.MethodGet, wsURL+"/pages", tt.AccessToken, nil, &listAfter)
	for _, p := range listAfter.Pages {
		require.NotEqual(t, created.ID, p.ID, "deleted page must not appear in list")
	}
}

// TestPageHierarchy verifies that child pages can be created under a
// parent and listed via the children endpoint.
func TestPageHierarchy(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	// Create parent page.
	var parent struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, wsURL+"/pages", tt.AccessToken,
		map[string]any{"title": "Parent"}, &parent)
	require.NotEmpty(t, parent.ID)

	// Create two children under the parent.
	var child1, child2 struct {
		ID           string  `json:"id"`
		ParentPageID *string `json:"parentPageId"`
	}
	doJSON(t, http.MethodPost, wsURL+"/pages", tt.AccessToken,
		map[string]any{"title": "Child A", "parentPageId": parent.ID}, &child1)
	doJSON(t, http.MethodPost, wsURL+"/pages", tt.AccessToken,
		map[string]any{"title": "Child B", "parentPageId": parent.ID}, &child2)
	require.NotNil(t, child1.ParentPageID)
	require.Equal(t, parent.ID, *child1.ParentPageID)

	// List children of the parent.
	var children struct {
		Total int64 `json:"total"`
		Pages []struct {
			ID string `json:"id"`
		} `json:"pages"`
	}
	doJSON(t, http.MethodGet, wsURL+"/pages/"+parent.ID+"/children",
		tt.AccessToken, nil, &children)
	require.Equal(t, int64(2), children.Total)
}

// TestPageSearch verifies that pages are searchable by title.
func TestPageSearch(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	wsURL := testServerURL + "/workspaces/" + tt.WorkspacePublicID

	// Create pages with distinct titles.
	doJSON(t, http.MethodPost, wsURL+"/pages", tt.AccessToken,
		map[string]any{"title": "Deployment Guide"}, nil)
	doJSON(t, http.MethodPost, wsURL+"/pages", tt.AccessToken,
		map[string]any{"title": "Architecture Overview"}, nil)

	// Search should find only the deployment page.
	var results struct {
		Total int64 `json:"total"`
		Pages []struct {
			Title string `json:"title"`
		} `json:"pages"`
	}
	doJSON(t, http.MethodGet, wsURL+"/pages/search?q=Deployment",
		tt.AccessToken, nil, &results)
	require.GreaterOrEqual(t, results.Total, int64(1))
	for _, p := range results.Pages {
		require.Contains(t, p.Title, "Deployment")
	}
}
