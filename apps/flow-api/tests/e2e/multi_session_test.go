package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMultipleSessionsTracked verifies that logging in from multiple
// clients creates distinct sessions, all visible in the session list.
func TestMultipleSessionsTracked(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Login again to create a second session.
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/auth/login", "",
		map[string]any{
			"email":    tt.Email,
			"password": tt.Password,
		}, &login)
	require.NotEmpty(t, login.AccessToken)

	// List sessions — should have at least 2 (register + login).
	var sessions struct {
		Items []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/sessions",
		tt.AccessToken, nil, &sessions)
	require.GreaterOrEqual(t, len(sessions.Items), 2,
		"must have at least 2 sessions after second login")

	// Each session has a unique ID.
	ids := map[string]bool{}
	for _, s := range sessions.Items {
		require.False(t, ids[s.ID], "session IDs must be unique")
		ids[s.ID] = true
	}
}

// TestRevokeSessionInvalidatesAccess verifies that revoking a session
// makes the associated access token unable to refresh.
func TestRevokeSessionInvalidatesAccess(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	tt := newTenant(t)

	// Login to get a second session with its own refresh cookie.
	_, body, resp := doRaw(t, http.MethodPost, testServerURL+"/auth/login", "",
		nil, map[string]any{
			"email":    tt.Email,
			"password": tt.Password,
		})
	require.Less(t, 0, len(body))

	secondCookie := pickCookie(resp, "nd_rt")
	require.NotNil(t, secondCookie)

	// List sessions from second login's token.
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	require.NoError(t, json.Unmarshal(body, &login))

	var sessions struct {
		Items []struct {
			ID      string `json:"id"`
			Current bool   `json:"current"`
		} `json:"items"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me/sessions",
		login.AccessToken, nil, &sessions)

	// Find the current session for this login.
	var currentID string
	for _, s := range sessions.Items {
		if s.Current {
			currentID = s.ID
			break
		}
	}
	require.NotEmpty(t, currentID)

	// Revoke it.
	doJSON(t, http.MethodDelete,
		testServerURL+"/me/sessions/"+currentID,
		login.AccessToken, nil, nil)

	// Refresh with the revoked session's cookie should fail.
	status, _, _ := doRaw(t, http.MethodPost, testServerURL+"/auth/refresh",
		"", []*http.Cookie{secondCookie}, nil)
	require.Equal(t, http.StatusUnauthorized, status,
		"refresh on revoked session must return 401")
}
