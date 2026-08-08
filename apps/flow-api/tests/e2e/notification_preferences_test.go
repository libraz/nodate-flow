package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
)

// preferenceCell is one row of the preferences matrix on the wire.
type preferenceCell struct {
	EventCategory string `json:"eventCategory"`
	Channel       string `json:"channel"`
	Muted         bool   `json:"muted"`
}

type preferencesBody struct {
	Preferences []preferenceCell `json:"preferences"`
}

// mutedCell reads one cell out of a matrix response.
func mutedCell(t *testing.T, body preferencesBody, category, channel string) bool {
	t.Helper()
	for _, p := range body.Preferences {
		if p.EventCategory == category && p.Channel == channel {
			return p.Muted
		}
	}
	t.Fatalf("no %s/%s cell in the preferences matrix", category, channel)
	return false
}

// TestNotificationPreferenceMuteStopsFanout is the load-bearing test for
// the notification settings: saving a mute has to stop delivery, not
// just return 200.
//
// The screen these preferences back used to write five booleans on the
// users row that nothing in the delivery path ever read, so every save
// succeeded and changed nothing. Asserting the response status would
// reproduce exactly that, so the assertion here is on the notifications
// table after a real event fans out.
//
// A second, unmuted category runs through the same fan-out afterwards.
// Without it, "mute works" and "fan-out is broken" produce identical
// results.
func TestNotificationPreferenceMuteStopsFanout(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	// Promote member to a real workspace member via the invite flow.
	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	require.NotEmpty(t, invite.Token)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	prefsURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/notification-preferences"

	// The member mutes task.mention on the in-app channel. Nothing else
	// is touched, so task.lifecycle stays at its default.
	var afterWrite preferencesBody
	doJSON(t, http.MethodPut, prefsURL, member.AccessToken, map[string]any{
		"preferences": []map[string]any{
			{"eventCategory": "task.mention", "channel": "in_app", "muted": true},
		},
	}, &afterWrite)
	require.True(t, mutedCell(t, afterWrite, "task.mention", "in_app"),
		"the write response must report the cell the caller just muted")
	require.False(t, mutedCell(t, afterWrite, "task.lifecycle", "in_app"),
		"an untouched category keeps the in-app default")

	// The setting survives a round trip, not just the write response.
	var afterRead preferencesBody
	doJSON(t, http.MethodGet, prefsURL, member.AccessToken, nil, &afterRead)
	require.True(t, mutedCell(t, afterRead, "task.mention", "in_app"))
	require.False(t, mutedCell(t, afterRead, "task.lifecycle", "in_app"))

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken,
		map[string]any{
			"projectId": owner.ProjectPublicID,
			"title":     "Preference-muted fanout target",
		}, &task)
	require.NotEmpty(t, task.ID)

	ctx := context.Background()
	wsInternalID := lookupWorkspaceInternalID(ctx, t, testDB, owner.WorkspacePublicID)
	ownerInternalID := lookupUserInternalID(ctx, t, testDB, owner.UserPublicID)
	memberInternalID := lookupUserInternalID(ctx, t, testDB, member.UserPublicID)

	queries := generated.New(testDB)
	f := notification.NewFanout(testDB, queries, email.NoopSender{})
	f.SetTimeout(5 * time.Second)

	// task.actor.added maps to the muted task.mention category.
	doJSON(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/actors",
		owner.AccessToken, map[string]any{
			"userId": member.UserPublicID,
			"role":   "assignee",
		}, nil)
	mentionEventID := latestEventID(ctx, t, wsInternalID, ownerInternalID, "task.actor.added")

	beforeMuted := notificationCountForUser(ctx, t, testDB, memberInternalID)
	hook := f.Hook()
	hook(ctx, wsInternalID, "task.actor.added", mentionEventID)
	require.NoError(t, f.Shutdown(ctxWithTimeout(t, 10*time.Second)))
	afterMuted := notificationCountForUser(ctx, t, testDB, memberInternalID)
	require.Equalf(t, int64(0), afterMuted-beforeMuted,
		"a muted category must produce no notification row (before=%d after=%d)",
		beforeMuted, afterMuted)

	// The same recipient, same fan-out, an unmuted category: this is
	// what proves the zero above came from the setting.
	doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+task.ID,
		owner.AccessToken, map[string]any{"title": "Preference-muted fanout target, renamed"}, nil)
	lifecycleEventID := latestEventID(ctx, t, wsInternalID, ownerInternalID, "task.updated")

	beforeUnmuted := notificationCountForUser(ctx, t, testDB, memberInternalID)
	f2 := notification.NewFanout(testDB, queries, email.NoopSender{})
	f2.SetTimeout(5 * time.Second)
	hook2 := f2.Hook()
	hook2(ctx, wsInternalID, "task.updated", lifecycleEventID)
	require.NoError(t, f2.Shutdown(ctxWithTimeout(t, 10*time.Second)))
	afterUnmuted := notificationCountForUser(ctx, t, testDB, memberInternalID)
	require.Equalf(t, int64(1), afterUnmuted-beforeUnmuted,
		"an unmuted category must still deliver (before=%d after=%d)",
		beforeUnmuted, afterUnmuted)
}

// TestNotificationPreferencesAreCallerScoped locks in that the endpoint
// reads and writes only the caller's own rows: two members of the same
// workspace configure the same category independently, and neither sees
// the other's value.
func TestNotificationPreferencesAreCallerScoped(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	member := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost,
		testServerURL+"/invites/"+invite.Token+"/accept",
		member.AccessToken, nil, nil)

	prefsURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/notification-preferences"

	var memberWrite preferencesBody
	doJSON(t, http.MethodPut, prefsURL, member.AccessToken, map[string]any{
		"preferences": []map[string]any{
			{"eventCategory": "ai", "channel": "in_app", "muted": true},
		},
	}, &memberWrite)
	require.True(t, mutedCell(t, memberWrite, "ai", "in_app"))

	var ownerRead preferencesBody
	doJSON(t, http.MethodGet, prefsURL, owner.AccessToken, nil, &ownerRead)
	require.False(t, mutedCell(t, ownerRead, "ai", "in_app"),
		"one member's mute must not appear on another member's matrix")
}

// TestNotificationPreferencesRejectUnknownCategory checks that an
// unrecognised category is refused rather than stored. A stored row for
// a category no client renders would be a setting the owner could never
// turn back off.
func TestNotificationPreferencesRejectUnknownCategory(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	prefsURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/notification-preferences"

	status, _ := doJSONStatus(t, http.MethodPut, prefsURL, owner.AccessToken, map[string]any{
		"preferences": []map[string]any{
			{"eventCategory": "not.a.category", "channel": "in_app", "muted": true},
		},
	})
	require.Equal(t, http.StatusUnprocessableEntity, status)

	var after preferencesBody
	doJSON(t, http.MethodGet, prefsURL, owner.AccessToken, nil, &after)
	for _, p := range after.Preferences {
		require.NotEqual(t, "not.a.category", p.EventCategory,
			"a rejected category must not reach the matrix")
	}
}

// TestNotificationPreferencesDenyNonMember checks the workspace boundary:
// the route is mounted behind workspace membership, so an outsider gets
// no view of anyone's settings.
func TestNotificationPreferencesDenyNonMember(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := newTenant(t)

	prefsURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/notification-preferences"

	status, body := doJSONStatus(t, http.MethodGet, prefsURL, outsider.AccessToken, nil)
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"a non-member reading another workspace's preferences")

	status, body = doJSONStatus(t, http.MethodPut, prefsURL, outsider.AccessToken, map[string]any{
		"preferences": []map[string]any{
			{"eventCategory": "ai", "channel": "in_app", "muted": true},
		},
	})
	requireDenied(t, status, body, http.StatusForbidden, "WS.WORKSPACE.ACCESS_DENIED",
		"a non-member writing another workspace's preferences")
}

// latestEventID returns the newest events.id of the given type for a
// (workspace, actor) pair. Anchoring on both keeps the lookup isolated
// from tests running in parallel against the same database.
func latestEventID(ctx context.Context, t *testing.T, wsInternalID, actorInternalID uint32, eventType string) uint64 {
	t.Helper()
	var id uint64
	err := testDB.QueryRowContext(ctx, `
		SELECT id
		FROM events
		WHERE workspace_id = ?
		  AND actor_user_id = ?
		  AND type = ?
		ORDER BY id DESC
		LIMIT 1
	`, wsInternalID, actorInternalID, eventType).Scan(&id)
	require.NoErrorf(t, err, "expected a %s event row", eventType)
	require.NotZero(t, id)
	return id
}
