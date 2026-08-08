package e2e

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestFavoriteCreateIsNotATaskExistenceOracle proves POST /me/favorites
// answers identically for a task that exists but is invisible to the caller
// and one that does not exist at all.
//
// A favorite is a per-user row: nobody else can see it and it grants no
// access, which is why the target check was left at workspace scope. The
// leak is not the row, it is the accept/reject split. A caller who can
// enumerate ids learns which ones name real tasks — one bit per guess, which
// is all an oracle needs, and enough to confirm that a task exists in a
// project they were deliberately not added to.
func TestFavoriteCreateIsNotATaskExistenceOracle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := seedGuestMember(t, owner)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
		"projectId":  owner.ProjectPublicID,
		"title":      "favorite-oracle-" + randomHex(8),
		"visibility": "private",
	}, &task)
	require.NotEmpty(t, task.ID)

	favorite := func(token, targetID string) (int, []byte) {
		return doJSONStatus(t, http.MethodPost, testServerURL+"/me/favorites", token, map[string]any{
			"workspaceId": owner.WorkspacePublicID,
			"targetType":  "task",
			"targetId":    targetID,
		})
	}

	invisibleStatus, invisibleBody := favorite(guest.AccessToken, task.ID)
	requireDenied(t, invisibleStatus, invisibleBody, http.StatusNotFound, "WS.FAVORITE.NOT_FOUND",
		"a guest favoriting a private task in a project they are not a member of")

	absentStatus, absentBody := favorite(guest.AccessToken, uuid.Must(uuid.NewV7()).String())
	requireDenied(t, absentStatus, absentBody, http.StatusNotFound, "WS.FAVORITE.NOT_FOUND",
		"a guest favoriting a task id that was never issued")

	require.Equalf(t, absentStatus, invisibleStatus,
		"an invisible task and an absent one must be indistinguishable; invisible=%s absent=%s",
		string(invisibleBody), string(absentBody))

	// The row must not exist either: a favorite the guest cannot explain
	// would surface the task id back to them through their own list.
	var listed struct {
		Favorites []struct {
			TargetID string `json:"targetId"`
		} `json:"favorites"`
	}
	doJSON(t, http.MethodGet,
		testServerURL+"/me/favorites?workspaceId="+owner.WorkspacePublicID,
		guest.AccessToken, nil, &listed)
	for _, f := range listed.Favorites {
		require.NotEqual(t, task.ID, f.TargetID,
			"a favorite was written for a task the guest may not see")
	}

	// Positive control: the owner, who can see the task, may favorite it.
	okStatus, okBody := favorite(owner.AccessToken, task.ID)
	require.Equalf(t, http.StatusOK, okStatus,
		"the task's creator must still be able to favorite it; body=%s", string(okBody))
}
