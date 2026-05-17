package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskTitleWhitespaceValidation(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var created struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "  Write release notes  ",
	}, &created)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "Write release notes", created.Title)

	status, _ := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "   ",
	})
	require.Equal(t, http.StatusUnprocessableEntity, status)

	var patched struct {
		Title string `json:"title"`
	}
	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+created.ID, tt.AccessToken, map[string]any{
		"title": "\tUpdated release notes\n",
	}, &patched)
	require.Equal(t, "Updated release notes", patched.Title)

	status, _ = doJSONStatus(t, http.MethodPatch, testServerURL+"/tasks/"+created.ID, tt.AccessToken, map[string]any{
		"title": "   ",
	})
	require.Equal(t, http.StatusUnprocessableEntity, status)
}
