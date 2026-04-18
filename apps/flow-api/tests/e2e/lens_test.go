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
		"sort":      json.RawMessage(`[{"field":"priority","dir":"desc"}]`),
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
// unauthenticated access and then unpublished to revoke it.
func TestLensPublishUnpublish(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/lenses"

	// Create a lens.
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, base, tt.AccessToken, map[string]any{
		"name":      "Public Board",
		"filter":    json.RawMessage(`{}`),
		"sort":      json.RawMessage(`[]`),
		"isDefault": false,
	}, &created)

	// Publish the lens.
	var published struct {
		PublicToken string `json:"publicToken"`
	}
	doJSON(t, http.MethodPost, base+"/"+created.ID+"/publish",
		tt.AccessToken, nil, &published)
	require.NotEmpty(t, published.PublicToken, "publish must return a token")

	// Public endpoint works without authentication.
	var pub struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/public/lenses/"+published.PublicToken,
		"", nil, &pub) // no bearer
	require.Equal(t, "Public Board", pub.Name)

	// Unpublish revokes access.
	doJSON(t, http.MethodPost, base+"/"+created.ID+"/unpublish",
		tt.AccessToken, nil, nil)

	// Public endpoint should now fail.
	status, _ := doJSONStatus(t, http.MethodGet,
		testServerURL+"/public/lenses/"+published.PublicToken, "", nil)
	require.GreaterOrEqual(t, status, 400, "unpublished lens must not be accessible")
}
