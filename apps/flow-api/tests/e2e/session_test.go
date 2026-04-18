package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSessionListShowsCurrentSession verifies that listing sessions
// returns at least one entry marked as "current".
func TestSessionListShowsCurrentSession(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	var resp struct {
		Items []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/sessions",
		tt.AccessToken, nil, &resp)

	require.GreaterOrEqual(t, len(resp.Items), 1, "must have at least one session")

	hasCurrentSession := false
	for _, s := range resp.Items {
		if s.Current {
			hasCurrentSession = true
		}
	}
	require.True(t, hasCurrentSession, "one session must be marked as current")
}

// TestCannotRevokeOtherUserSession verifies that user A cannot revoke
// user B's session even if A knows the session public ID.
func TestCannotRevokeOtherUserSession(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	userA := newTenant(t)
	userB := newTenant(t)

	// Get user B's session ID.
	var sessionsB struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/sessions",
		userB.AccessToken, nil, &sessionsB)
	require.GreaterOrEqual(t, len(sessionsB.Items), 1)
	targetSessionID := sessionsB.Items[0].ID

	// User A tries to revoke user B's session.
	doJSONStatus(t, http.MethodDelete,
		testServerURL+"/me/sessions/"+targetSessionID,
		userA.AccessToken, nil)

	// User B's session must still be valid.
	status, _ := doJSONStatus(t, http.MethodGet, testServerURL+"/me",
		userB.AccessToken, nil)
	require.Equal(t, http.StatusOK, status,
		"user B's session must survive user A's revocation attempt")
}

// TestRevokeOwnSession verifies that a user can revoke one of their
// own sessions and it disappears from the session list.
func TestRevokeOwnSession(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// List sessions.
	var sessions struct {
		Items []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/sessions",
		tt.AccessToken, nil, &sessions)
	require.GreaterOrEqual(t, len(sessions.Items), 1)

	// Find the current session and revoke it.
	var currentID string
	for _, s := range sessions.Items {
		if s.Current {
			currentID = s.ID
			break
		}
	}
	require.NotEmpty(t, currentID)

	var result struct {
		Ok bool `json:"ok"`
	}
	doJSON(t, http.MethodDelete,
		testServerURL+"/me/sessions/"+currentID,
		tt.AccessToken, nil, &result)
	require.True(t, result.Ok)
}
