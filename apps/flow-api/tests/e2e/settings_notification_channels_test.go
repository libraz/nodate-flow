package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNotificationPreferencesRefuseChannelsWithoutTransport pins that a
// preference can only be stored for a channel something delivers. Only the
// in-app bell has a transport: an enabled email or push row would never be
// sent and would list every notification in the bell a second time.
func TestNotificationPreferencesRefuseChannelsWithoutTransport(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	prefsURL := testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/notification-preferences"

	for _, channel := range []string{"email", "push"} {
		status, _ := doJSONStatus(t, http.MethodPut, prefsURL, owner.AccessToken, map[string]any{
			"preferences": []map[string]any{
				{"eventCategory": "task.comment", "channel": channel, "muted": false},
			},
		})
		require.Equal(t, http.StatusUnprocessableEntity, status, "channel %s has no transport", channel)
	}

	status, _ := doJSONStatus(t, http.MethodPut, prefsURL, owner.AccessToken, map[string]any{
		"preferences": []map[string]any{
			{"eventCategory": "task.comment", "channel": "in_app", "muted": true},
		},
	})
	require.Equal(t, http.StatusOK, status)

	var after preferencesBody
	doJSON(t, http.MethodGet, prefsURL, owner.AccessToken, nil, &after)
	for _, channel := range []string{"email", "push"} {
		require.True(t, mutedCell(t, after, "task.comment", channel), "a refused %s write must leave the channel off", channel)
	}
	require.True(t, mutedCell(t, after, "task.comment", "in_app"), "the in_app mute was stored")
}
