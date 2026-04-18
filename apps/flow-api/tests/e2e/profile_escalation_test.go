package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProfileUpdateIgnoresRoleField verifies that a user cannot inject
// a "role" field via PATCH /me to escalate privileges. The API must
// silently ignore unknown fields.
func TestProfileUpdateIgnoresRoleField(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Attempt to inject role via profile update — Huma rejects unknown
	// fields, so the request should fail (4xx), not succeed with escalation.
	status, _ := doJSONStatus(t, http.MethodPatch, testServerURL+"/me",
		tt.AccessToken, map[string]any{
			"role":    "superadmin",
			"isAdmin": true,
		})
	require.Less(t, status, 500, "role injection must not cause 500")

	// Regardless of acceptance or rejection, verify no role/admin
	// field appears in the user profile response.
	_, body := doJSONStatus(t, http.MethodGet, testServerURL+"/me",
		tt.AccessToken, nil)
	require.NotContains(t, string(body), `"role"`,
		"GET /me must not expose a role field")
	require.NotContains(t, string(body), `"isAdmin"`,
		"GET /me must not expose an isAdmin field")
}

// TestProfileUpdateRejectsOverlongDisplayName verifies that display
// name length is validated.
func TestProfileUpdateRejectsOverlongDisplayName(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	longName := make([]byte, 101)
	for i := range longName {
		longName[i] = 'a'
	}

	status, _ := doJSONStatus(t, http.MethodPatch, testServerURL+"/me",
		tt.AccessToken, map[string]any{"displayName": string(longName)})
	require.GreaterOrEqual(t, status, 400,
		"display name over 100 chars must be rejected")
}

// TestWorkspaceSlugCollision verifies that creating two workspaces
// with the same slug returns an error.
func TestWorkspaceSlugCollision(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	slug := "collision-" + randomHex(4)

	// First workspace succeeds.
	doJSON(t, http.MethodPost, testServerURL+"/workspaces", tt.AccessToken,
		map[string]any{"slug": slug, "name": "First WS"}, nil)

	// Second workspace with same slug must fail.
	status, _ := doJSONStatus(t, http.MethodPost, testServerURL+"/workspaces",
		tt.AccessToken, map[string]any{"slug": slug, "name": "Second WS"})
	require.GreaterOrEqual(t, status, 400,
		"duplicate workspace slug must be rejected")
	require.Less(t, status, 500,
		"slug collision must return 4xx, not 500")
}

// TestPaginationTotalAccuracy verifies that list endpoints return
// accurate total counts and respect limit/offset parameters.
func TestPaginationTotalAccuracy(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	base := testServerURL + "/workspaces/" + tt.WorkspacePublicID + "/pages"

	// Create 5 pages.
	for i := 0; i < 5; i++ {
		doJSON(t, http.MethodPost, base, tt.AccessToken,
			map[string]any{"title": "Page " + randomHex(2)}, nil)
	}

	// Full list.
	var full struct {
		Total int64 `json:"total"`
		Pages []struct {
			ID string `json:"id"`
		} `json:"pages"`
	}
	doJSON(t, http.MethodGet, base, tt.AccessToken, nil, &full)
	require.GreaterOrEqual(t, full.Total, int64(5))
	require.GreaterOrEqual(t, len(full.Pages), 5)

	// Paginated: limit=2.
	var page1 struct {
		Total int64 `json:"total"`
		Pages []struct {
			ID string `json:"id"`
		} `json:"pages"`
	}
	doJSON(t, http.MethodGet, base+"?limit=2", tt.AccessToken, nil, &page1)
	require.Equal(t, full.Total, page1.Total, "total must be same regardless of limit")
	require.Equal(t, 2, len(page1.Pages), "limit=2 must return 2 items")

	// Paginated: offset=3, limit=2.
	var page2 struct {
		Total int64 `json:"total"`
		Pages []struct {
			ID string `json:"id"`
		} `json:"pages"`
	}
	doJSON(t, http.MethodGet, base+"?limit=2&offset=3", tt.AccessToken, nil, &page2)
	require.Equal(t, full.Total, page2.Total, "total must be same with offset")
	require.LessOrEqual(t, len(page2.Pages), 2)

	// Pages from page1 and page2 must not overlap.
	for _, p1 := range page1.Pages {
		for _, p2 := range page2.Pages {
			require.NotEqual(t, p1.ID, p2.ID,
				"paginated results must not overlap")
		}
	}
}
