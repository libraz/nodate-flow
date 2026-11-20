package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type listedFavorite struct {
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetId"`
	Title      string `json:"title"`
}

func listFavorites(t *testing.T, token, wsID string) map[string]listedFavorite {
	t.Helper()
	var out struct {
		Favorites []listedFavorite `json:"favorites"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/favorites?workspaceId="+wsID, token, nil, &out)
	byID := map[string]listedFavorite{}
	for _, f := range out.Favorites {
		byID[f.TargetID] = f
	}
	return byID
}

func addFavorite(t *testing.T, token, wsID, targetType, targetID string) {
	t.Helper()
	status, body := doJSONStatus(t, http.MethodPost, testServerURL+"/me/favorites", token, map[string]any{
		"workspaceId": wsID, "targetType": targetType, "targetId": targetID,
	})
	require.Equalf(t, http.StatusOK, status, "favorite %s %s: %s", targetType, targetID, string(body))
}

// TestFavoriteListTitlePerTargetType pins that the list names each target
// by its own title, whatever the target type.
func TestFavoriteListTitlePerTargetType(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	taskTitle := "favorite-title-task-" + randomHex(4)
	pageTitle := "favorite-title-page-" + randomHex(4)
	task := createTaskWithVisibility(t, tt, taskTitle, "public")

	var page struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/pages", tt.AccessToken,
		map[string]any{"title": pageTitle, "body": "body"}, &page)
	require.NotEmpty(t, page.ID)

	addFavorite(t, tt.AccessToken, tt.WorkspacePublicID, "task", task)
	addFavorite(t, tt.AccessToken, tt.WorkspacePublicID, "page", page.ID)
	addFavorite(t, tt.AccessToken, tt.WorkspacePublicID, "project", tt.ProjectPublicID)

	got := listFavorites(t, tt.AccessToken, tt.WorkspacePublicID)
	require.Equal(t, taskTitle, got[task].Title)
	require.Equal(t, pageTitle, got[page.ID].Title)
	require.Equal(t, "Test Project "+tt.ProjectSlug[len("prj-"):], got[tt.ProjectPublicID].Title)

	// A renamed task shows its new title: the title is read at list time.
	renamed := taskTitle + "-renamed"
	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+task, tt.AccessToken,
		map[string]any{"title": renamed}, nil)
	require.Equal(t, renamed, listFavorites(t, tt.AccessToken, tt.WorkspacePublicID)[task].Title)
}

// TestFavoriteTitleHiddenWithTask pins that a favorited task which later
// becomes private to someone else gives the caller no title (the row
// either drops or carries an empty title), while its creator still gets
// the title.
func TestFavoriteTitleHiddenWithTask(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := seedWorkspaceMemberTenant(t, owner)
	title := "favorite-hidden-title-" + randomHex(4)
	task := createTaskWithVisibility(t, owner, title, "public")

	addFavorite(t, member.AccessToken, owner.WorkspacePublicID, "task", task)
	require.Equal(t, title, listFavorites(t, member.AccessToken, owner.WorkspacePublicID)[task].Title,
		"fixture: the member sees the title while the task is public")

	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+task, owner.AccessToken,
		map[string]any{"visibility": "private"}, nil)

	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/me/favorites?workspaceId="+owner.WorkspacePublicID, member.AccessToken, nil)
	require.Equal(t, http.StatusOK, status)
	require.NotContains(t, string(body), title, "the hidden task's title must not reach the member")

	addFavorite(t, owner.AccessToken, owner.WorkspacePublicID, "task", task)
	require.Equal(t, title, listFavorites(t, owner.AccessToken, owner.WorkspacePublicID)[task].Title)
}
