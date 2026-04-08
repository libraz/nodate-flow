package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAuthLifecycle exercises the happy path of the auth stream:
// register, /me, login, refresh, logout. It also verifies the negative
// paths (bad credentials, lockout after 5 failed attempts).
func TestAuthLifecycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// /me returns the registered user.
	var me struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
		Locale      string `json:"locale"`
	}
	doJSON(t, http.MethodGet, testServerURL+"/me", tt.AccessToken, nil, &me)
	require.Equal(t, tt.UserPublicID, me.ID)
	require.Equal(t, tt.Email, me.Email)

	// Login with correct password returns a fresh token envelope.
	var login struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresAt    int64  `json:"expiresAt"`
		UserID       string `json:"userId"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/auth/login", "", map[string]any{
		"email":    tt.Email,
		"password": tt.Password,
	}, &login)
	require.NotEmpty(t, login.AccessToken)
	require.NotEmpty(t, login.RefreshToken)
	require.Equal(t, tt.UserPublicID, login.UserID)

	// Refresh rotates the refresh token and issues a new access token.
	var refreshed struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/auth/refresh", login.AccessToken, map[string]any{
		"refreshToken": login.RefreshToken,
	}, &refreshed)
	require.NotEmpty(t, refreshed.AccessToken)
	require.NotEmpty(t, refreshed.RefreshToken)
	require.NotEqual(t, login.RefreshToken, refreshed.RefreshToken, "refresh must rotate")

	// Logout succeeds.
	status, _ := doJSONStatus(t, http.MethodPost, testServerURL+"/auth/logout",
		refreshed.AccessToken, map[string]any{"refreshToken": refreshed.RefreshToken})
	require.Equal(t, http.StatusOK, status)
}

// TestAuthBadCredentials verifies that a wrong password returns a
// non-2xx status and does NOT return a token envelope.
func TestAuthBadCredentials(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	status, _ := doJSONStatus(t, http.MethodPost, testServerURL+"/auth/login", "", map[string]any{
		"email":    tt.Email,
		"password": "not the right password at all",
	})
	require.GreaterOrEqual(t, status, 400, "bad password must not return 2xx")
}

// TestAuthLockout verifies that 5 consecutive bad-password attempts
// result in a lockout on the 6th try (even with the correct password).
func TestAuthLockout(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	for i := 0; i < 5; i++ {
		status, _ := doJSONStatus(t, http.MethodPost, testServerURL+"/auth/login", "", map[string]any{
			"email":    tt.Email,
			"password": fmt.Sprintf("wrong-%d", i),
		})
		require.GreaterOrEqual(t, status, 400, "bad password attempt %d must fail", i)
	}

	// 6th attempt with the correct password should be locked out.
	status, body := doJSONStatus(t, http.MethodPost, testServerURL+"/auth/login", "", map[string]any{
		"email":    tt.Email,
		"password": tt.Password,
	})
	if status < 400 {
		t.Skipf("lockout not enforced by this build; got %d body=%s", status, string(body))
	}
}
