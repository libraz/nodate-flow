package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"image/color"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/packages/go-shared/avatarutil"
)

// avatarRow finds the element of list[listKey] whose idKey equals userID and
// returns its avatarKey value (nil when the field is omitted or null).
func avatarRow(t *testing.T, token, endpoint, listKey, idKey, avatarKey, userID string) *string {
	t.Helper()
	var body map[string]any
	doJSON(t, http.MethodGet, endpoint, token, nil, &body)
	rows, ok := body[listKey].([]any)
	require.Truef(t, ok, "%s: response has no %q array: %v", endpoint, listKey, body)
	for _, r := range rows {
		row, ok := r.(map[string]any)
		require.True(t, ok)
		if row[idKey] != userID {
			continue
		}
		return stringField(row, avatarKey)
	}
	require.FailNowf(t, "row not found", "%s: no %s row with %s=%s", endpoint, listKey, idKey, userID)
	return nil
}

func stringField(row map[string]any, key string) *string {
	v, ok := row[key].(string)
	if !ok {
		return nil
	}
	return &v
}

// TestAvatarProjectionAcrossUserEndpoints guards the rule that every endpoint
// projecting a user resolves the avatar from the uploaded storage object, not
// only from users.avatar_url (which an upload clears).
func TestAvatarProjectionAcrossUserEndpoints(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	owner := newTenant(t)
	plain := inviteAndJoinWorkspace(t, owner)
	wsBase := testServerURL + "/workspaces/" + owner.WorkspacePublicID

	// Project membership for the avatar-less user (negative case).
	doJSON(t, http.MethodPost, testServerURL+"/projects/"+owner.ProjectPublicID+"/members",
		owner.AccessToken, map[string]any{"userId": plain.UserPublicID, "role": "editor"}, nil)

	taskID := createTaskForAttachment(t, owner.AccessToken, owner.ProjectPublicID, "Avatar projection")
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/actors", owner.AccessToken,
		map[string]any{"userId": plain.UserPublicID, "role": "assignee"}, nil)
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+taskID+"/comments", owner.AccessToken,
		map[string]any{"body": "avatar projection comment"}, nil)

	calID := createCalendarForTenant(t, owner.AccessToken, owner.WorkspacePublicID, "Avatar projection")
	start := time.Date(2027, 8, 1, 9, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/calendars/%s/events", wsBase, calID), owner.AccessToken,
		map[string]any{
			"kind": "event", "title": "Avatar projection event",
			"startAt": start.Unix(), "endAt": start.Add(time.Hour).Unix(), "timezone": "UTC",
		}, &evt)
	require.NotEmpty(t, evt.ID)
	evtBase := fmt.Sprintf("%s/calendars/%s/events/%s", wsBase, calID, evt.ID)

	doJSON(t, http.MethodPost, fmt.Sprintf("%s/calendars/%s/members", wsBase, calID), owner.AccessToken,
		map[string]any{"email": plain.Email, "role": "viewer"}, nil)
	doJSON(t, http.MethodPost, evtBase+"/attendees", owner.AccessToken,
		map[string]any{"userIds": []string{owner.UserPublicID, plain.UserPublicID}}, nil)
	doJSON(t, http.MethodPost, evtBase+"/comments", owner.AccessToken,
		map[string]any{"body": "avatar projection event comment"}, nil)

	// Upload the avatar after every row exists so each endpoint must resolve
	// it at read time.
	png := makePNG(t, 8, 8, color.RGBA{R: 40, G: 100, B: 220, A: 255})
	_, status := uploadAvatar(t, owner.AccessToken, "projection.png", "image/png", png)
	require.Equal(t, http.StatusOK, status)
	_, objectID := userInternalIDAndAvatar(t, testDB, owner.UserPublicID)
	require.True(t, objectID.Valid, "upload must set avatar_storage_object_id")
	require.Positive(t, objectID.Int32)
	wantTag := avatarutil.OpaqueTag(uint64(objectID.Int32)) //#nosec G115 -- asserted positive above
	wantPath := "/avatars/" + owner.UserPublicID

	rangeQ := "?start=2027-07-01&end=2027-09-01"
	cases := []struct {
		name                                string
		endpoint, listKey, idKey, avatarKey string
	}{
		{"project members", testServerURL + "/projects/" + owner.ProjectPublicID + "/members", "members", "userId", "avatarUrl"},
		{"task actors", testServerURL + "/tasks/" + taskID + "/actors", "actors", "userId", "avatarUrl"},
		{"task comments", testServerURL + "/tasks/" + taskID + "/comments", "comments", "authorId", "authorAvatarUrl"},
		{"calendar members", fmt.Sprintf("%s/calendars/%s/members", wsBase, calID), "members", "userId", "avatarUrl"},
		{"event attendees", evtBase + "/attendees", "attendees", "userId", "avatarUrl"},
		{"event comments", evtBase + "/comments", "comments", "userId", "avatarUrl"},
		{"event range list creator", fmt.Sprintf("%s/calendars/%s/events%s", wsBase, calID, rangeQ), "events", "creatorId", "creatorAvatarUrl"},
		{"workspace users", wsBase + "/users", "users", "id", "avatarUrl"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := avatarRow(t, owner.AccessToken, c.endpoint, c.listKey, c.idKey, c.avatarKey, owner.UserPublicID)
			requireProxyAvatar(t, got, wantPath, wantTag)
		})
	}

	t.Run("event get creator", func(t *testing.T) {
		t.Parallel()
		var body map[string]any
		doJSON(t, http.MethodGet, evtBase, owner.AccessToken, nil, &body)
		require.Equal(t, owner.UserPublicID, body["creatorId"])
		requireProxyAvatar(t, stringField(body, "creatorAvatarUrl"), wantPath, wantTag)
	})

	negatives := []struct {
		name                                string
		endpoint, listKey, idKey, avatarKey string
	}{
		{"project members", cases[0].endpoint, "members", "userId", "avatarUrl"},
		{"task actors", cases[1].endpoint, "actors", "userId", "avatarUrl"},
		{"calendar members", cases[3].endpoint, "members", "userId", "avatarUrl"},
		{"event attendees", cases[4].endpoint, "attendees", "userId", "avatarUrl"},
		{"workspace users", cases[7].endpoint, "users", "id", "avatarUrl"},
	}
	for _, c := range negatives {
		t.Run("no avatar omitted/"+c.name, func(t *testing.T) {
			t.Parallel()
			got := avatarRow(t, owner.AccessToken, c.endpoint, c.listKey, c.idKey, c.avatarKey, plain.UserPublicID)
			require.Nil(t, got, "a user without an avatar must not get a proxy URL")
		})
	}
}

func requireProxyAvatar(t *testing.T, got *string, wantPath, wantTag string) {
	t.Helper()
	require.NotNil(t, got, "uploaded avatar must be projected")
	u, err := url.Parse(*got)
	require.NoError(t, err)
	require.Equal(t, wantPath, u.Path)
	require.NotEmpty(t, u.Query().Get("v"))
	require.Equal(t, wantTag, u.Query().Get("v"))
}
