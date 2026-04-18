package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNotificationUnreadCount verifies that the unread notification
// count endpoint returns a valid response for an authenticated user.
func TestNotificationUnreadCount(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var count struct {
		UnreadCount int64 `json:"unreadCount"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/notifications/unread-count",
		tt.AccessToken, nil, &count)
	// A fresh tenant has zero unread notifications.
	require.Equal(t, int64(0), count.UnreadCount)
}

// TestNotificationListEmpty verifies that listing notifications for a
// fresh tenant returns an empty list with total zero.
func TestNotificationListEmpty(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var list struct {
		Total         int64 `json:"total"`
		Notifications []struct {
			ID string `json:"id"`
		} `json:"notifications"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/notifications",
		tt.AccessToken, nil, &list)
	require.Equal(t, int64(0), list.Total)
	require.Empty(t, list.Notifications)
}

// TestNotificationMarkAllRead verifies that the mark-all-read endpoint
// succeeds even when there are no notifications.
func TestNotificationMarkAllRead(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var result struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/notifications/read-all",
		tt.AccessToken, nil, &result)
	require.True(t, result.Ok)
}
