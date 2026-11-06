package e2e

import (
	"context"
	"image"
	"image/color"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/packages/go-shared/avatarutil"
)

type timelineAvatarEvent struct {
	ID             string  `json:"id"`
	Type           string  `json:"type"`
	TaskID         string  `json:"taskId"`
	ActorUserID    string  `json:"actorUserId"`
	ActorAvatarURL *string `json:"actorAvatarUrl"`
}

type timelineAvatarResponse struct {
	Events []timelineAvatarEvent `json:"events"`
}

func fetchActorTimelineAvatar(t *testing.T, tenantUserID, token, timelineURL string) []timelineAvatarEvent {
	t.Helper()

	var out timelineAvatarResponse
	separator := "?"
	if strings.Contains(timelineURL, "?") {
		separator = "&"
	}
	doJSON(t, http.MethodGet, timelineURL+separator+"actor="+url.QueryEscape(tenantUserID), token, nil, &out)
	return out.Events
}

func requireTimelineAvatar(t *testing.T, events []timelineAvatarEvent, actorUserID string) *string {
	t.Helper()
	for _, event := range events {
		if event.ActorUserID == actorUserID {
			return event.ActorAvatarURL
		}
	}
	require.FailNow(t, "actor timeline event not found", "actor user id: %s", actorUserID)
	return nil
}

func TestTimelineAvatarProjectionAcrossScopes(t *testing.T) {
	bootstrap(t)
	requireStorage(t)
	t.Parallel()

	tenant := newTenant(t)
	taskID := createTaskForAttachment(t, tenant.AccessToken, tenant.ProjectPublicID, "Timeline avatar projection")
	timelineURLs := []struct {
		name string
		url  string
	}{
		{name: "task", url: testServerURL + "/tasks/" + taskID + "/timeline"},
		{name: "project", url: testServerURL + "/projects/" + tenant.ProjectPublicID + "/timeline"},
		{name: "workspace", url: testServerURL + "/workspaces/" + tenant.WorkspacePublicID + "/timeline"},
	}

	for _, scope := range timelineURLs {
		t.Run(scope.name+" without avatar", func(t *testing.T) {
			avatarURL := requireTimelineAvatar(t,
				fetchActorTimelineAvatar(t, tenant.UserPublicID, tenant.AccessToken, scope.url),
				tenant.UserPublicID,
			)
			require.Nil(t, avatarURL, "a user without an uploaded or external avatar must not be sent through the proxy")
		})
	}

	// The timeline must keep external provider URLs verbatim. The avatar
	// proxy remains an uploaded-image endpoint and still 404s for this user.
	externalURL := "https://images.example.test/avatar.png?size=96"
	uid := internalUserID(t, testDB, tenant.UserPublicID)
	_, err := testDB.ExecContext(context.Background(), `UPDATE users SET avatar_url = ? WHERE id = ?`, externalURL, uid)
	require.NoError(t, err)
	for _, scope := range timelineURLs {
		avatarURL := requireTimelineAvatar(t,
			fetchActorTimelineAvatar(t, tenant.UserPublicID, tenant.AccessToken, scope.url),
			tenant.UserPublicID,
		)
		require.NotNil(t, avatarURL)
		require.Equal(t, externalURL, *avatarURL, scope.name)
	}
	proxyStatus, _ := doJSONStatus(t, http.MethodGet, testServerURL+"/avatars/"+tenant.UserPublicID, "", nil)
	require.Equal(t, http.StatusNotFound, proxyStatus, "the proxy must not fetch an external avatar_url")

	// Use the real avatar upload endpoint and disposable storage. Even if a
	// stale external value is present beside the FK, the uploaded object is
	// authoritative and its public URL carries an opaque cache tag.
	png := makePNG(t, 8, 8, color.RGBA{R: 40, G: 100, B: 220, A: 255})
	_, uploadStatus := uploadAvatar(t, tenant.AccessToken, "timeline-avatar.png", "image/png", png)
	require.Equal(t, http.StatusOK, uploadStatus)
	_, avatarObjectID := userInternalIDAndAvatar(t, testDB, tenant.UserPublicID)
	require.True(t, avatarObjectID.Valid, "upload must set avatar_storage_object_id")
	var uploadedObjectTag string
	if avatarObjectID.Int32 > 0 {
		uploadedObjectTag = avatarutil.OpaqueTag(uint64(avatarObjectID.Int32))
	} else {
		t.Fatalf("avatar storage object id %d must be positive", avatarObjectID.Int32)
	}
	_, err = testDB.ExecContext(context.Background(), `UPDATE users SET avatar_url = ? WHERE id = ?`, externalURL, uid)
	require.NoError(t, err)

	var uploadedURL string
	for _, scope := range timelineURLs {
		avatarURL := requireTimelineAvatar(t,
			fetchActorTimelineAvatar(t, tenant.UserPublicID, tenant.AccessToken, scope.url),
			tenant.UserPublicID,
		)
		require.NotNil(t, avatarURL)
		u, err := url.Parse(*avatarURL)
		require.NoError(t, err)
		require.Equal(t, "/avatars/"+tenant.UserPublicID, u.Path)
		require.Equal(t, uploadedObjectTag, u.Query().Get("v"))
		if uploadedURL == "" {
			uploadedURL = *avatarURL
		} else {
			require.Equal(t, uploadedURL, *avatarURL, "all timeline scopes must expose one canonical avatar URL")
		}
	}

	proxyResp, err := http.Get(testServerURL + uploadedURL)
	require.NoError(t, err)
	defer func() { _ = proxyResp.Body.Close() }()
	require.Equal(t, http.StatusOK, proxyResp.StatusCode)
	require.Equal(t, "image/png", proxyResp.Header.Get("Content-Type"))
	decoded, format, err := image.Decode(proxyResp.Body)
	require.NoError(t, err, "uploaded timeline avatar must decode")
	require.Equal(t, "png", format)
	require.Equal(t, 8, decoded.Bounds().Dx())
	require.Equal(t, 8, decoded.Bounds().Dy())
}

func TestTimelineAvatarProjectionOmitsDisabledActor(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	actor := seedProjectRoleMember(t, owner, "editor")
	taskID := createTaskForAttachment(t, actor.AccessToken, owner.ProjectPublicID, "Disabled timeline actor")
	actorID := internalUserID(t, testDB, actor.UserPublicID)
	_, err := testDB.ExecContext(context.Background(), `UPDATE users SET enabled = FALSE WHERE id = ?`, actorID)
	require.NoError(t, err)

	var timeline timelineAvatarResponse
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/timeline",
		owner.AccessToken, nil, &timeline)

	for _, event := range timeline.Events {
		if event.Type == "task.created" && event.TaskID == taskID && event.ActorUserID == "" {
			require.Nil(t, event.ActorAvatarURL, "disabled actor joins must not expose avatar metadata")
			return
		}
	}
	require.FailNow(t, "task.created event for disabled actor not found", "task id: %s", taskID)
}
