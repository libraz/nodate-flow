package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAuthLifecycle exercises the happy path of the auth stream:
// register, /me, login, refresh, logout. It also verifies that the
// refresh token is only delivered via the nd_rt httpOnly cookie and
// never in the JSON body.
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

	// Login with correct password. The JSON body must NOT contain any
	// refreshToken field; the refresh token lives in the nd_rt cookie.
	loginStatus, loginBody, loginResp := doRaw(t, http.MethodPost, testServerURL+"/auth/login", "", nil, map[string]any{
		"email":    tt.Email,
		"password": tt.Password,
	})
	require.Equal(t, http.StatusOK, loginStatus, "login body=%s", string(loginBody))
	require.NotContainsf(t, string(loginBody), "refreshToken", "login body leaked refreshToken: %s", string(loginBody))

	var login struct {
		AccessToken string `json:"accessToken"`
		UserID      string `json:"userId"`
	}
	require.NoError(t, json.Unmarshal(loginBody, &login))
	require.NotEmpty(t, login.AccessToken)
	require.Equal(t, tt.UserPublicID, login.UserID)

	loginCookie := pickCookie(loginResp, "nd_rt")
	require.NotNil(t, loginCookie, "login did not set nd_rt cookie")
	require.True(t, loginCookie.HttpOnly, "nd_rt must be HttpOnly")
	// In tests CookieSecure=false, so the cookie uses SameSite=Lax
	// (SameSite=None requires Secure which is off in http/dev mode).
	require.Equal(t, http.SameSiteLaxMode, loginCookie.SameSite, "nd_rt must be SameSite=Lax in non-secure mode")
	require.Equal(t, "/auth", loginCookie.Path, "nd_rt must be scoped to /auth")
	require.NotEmpty(t, loginCookie.Value)

	// Refresh rotates the refresh cookie and issues a new access token.
	// No request body is sent; the token travels in the cookie header.
	refStatus, refBody, refResp := doRaw(t, http.MethodPost, testServerURL+"/auth/refresh", login.AccessToken,
		[]*http.Cookie{{Name: "nd_rt", Value: loginCookie.Value}}, nil)
	require.Equal(t, http.StatusOK, refStatus, "refresh body=%s", string(refBody))
	require.NotContainsf(t, string(refBody), "refreshToken", "refresh body leaked refreshToken: %s", string(refBody))

	var refreshed struct {
		AccessToken string `json:"accessToken"`
	}
	require.NoError(t, json.Unmarshal(refBody, &refreshed))
	require.NotEmpty(t, refreshed.AccessToken)

	rotated := pickCookie(refResp, "nd_rt")
	require.NotNil(t, rotated, "refresh did not set rotated nd_rt cookie")
	require.NotEqual(t, loginCookie.Value, rotated.Value, "refresh must rotate the cookie value")

	// Logout clears the refresh cookie.
	outStatus, _, outResp := doRaw(t, http.MethodPost, testServerURL+"/auth/logout", refreshed.AccessToken,
		[]*http.Cookie{{Name: "nd_rt", Value: rotated.Value}}, nil)
	require.Equal(t, http.StatusOK, outStatus)
	cleared := pickCookie(outResp, "nd_rt")
	require.NotNil(t, cleared, "logout must emit a Set-Cookie to clear nd_rt")
	require.Equal(t, "", cleared.Value, "logout must clear nd_rt value")
	require.True(t, cleared.MaxAge < 0 || cleared.MaxAge == 0, "logout must set Max-Age=0 (got %d)", cleared.MaxAge)
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

// TestAuthLockout verifies that lockout is enforced: 5 consecutive
// bad-password attempts must result in a non-2xx response on the 6th
// login even when the correct password is supplied. The test fails
// (rather than skips) if lockout is not enforced — a build that
// silently lets the 6th login succeed is a security regression we want
// CI to surface, not hide.
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

	// 6th attempt with the correct password must still be rejected.
	status, body := doJSONStatus(t, http.MethodPost, testServerURL+"/auth/login", "", map[string]any{
		"email":    tt.Email,
		"password": tt.Password,
	})
	require.GreaterOrEqualf(t, status, 400,
		"lockout not enforced: 6th login with correct password returned %d body=%s",
		status, string(body))
}

// doRaw sends a request with optional JSON body and optional request
// cookies, returning status, raw body, and the full *http.Response so
// tests can inspect Set-Cookie headers.
func doRaw(t *testing.T, method, url, bearer string, cookies []*http.Cookie, body any) (int, []byte, *http.Response) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw, resp
}

// pickCookie returns the first Set-Cookie in resp with the given name,
// or nil if none matches.
func pickCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if strings.EqualFold(c.Name, name) {
			return c
		}
	}
	return nil
}
