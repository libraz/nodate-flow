package integrations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Registry ---

func TestNewRegistry_SkipsUnconfiguredProviders(t *testing.T) {
	t.Parallel()
	r := NewRegistry(
		func() (Provider, error) { return nil, ErrNotConfigured },
		func() (Provider, error) { return nil, ErrNotConfigured },
	)
	require.NotNil(t, r)
	assert.Empty(t, r.Names(), "registry must contain zero providers when all candidates fail")
}

func TestNewRegistry_KeepsConfiguredProviders(t *testing.T) {
	t.Parallel()
	r := NewRegistry(
		func() (Provider, error) { return &stubProvider{name: "alpha"}, nil },
		func() (Provider, error) { return nil, ErrNotConfigured },
		func() (Provider, error) { return &stubProvider{name: "beta"}, nil },
	)
	assert.True(t, r.Has("alpha"))
	assert.True(t, r.Has("beta"))
	assert.False(t, r.Has("gamma"), "non-registered provider must not be found")
	assert.Len(t, r.Names(), 2)
}

func TestNewRegistry_NilProviderIsSkipped(t *testing.T) {
	t.Parallel()
	r := NewRegistry(func() (Provider, error) { return nil, nil })
	assert.Empty(t, r.Names())
}

func TestRegistry_GetReturnsCorrectProvider(t *testing.T) {
	t.Parallel()
	p := &stubProvider{name: "github"}
	r := NewRegistry(func() (Provider, error) { return p, nil })
	got, err := r.Get("github")
	require.NoError(t, err)
	assert.Equal(t, p, got, "Get must return the exact registered instance")
}

func TestRegistry_GetMissingReturnsErrNotConfigured(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_, err := r.Get("github")
	require.ErrorIs(t, err, ErrNotConfigured)
}

func TestRegistry_NilReceiverIsSafe(t *testing.T) {
	t.Parallel()
	var r *Registry
	assert.False(t, r.Has("github"), "nil receiver Has must return false")
	_, err := r.Get("github")
	require.ErrorIs(t, err, ErrNotConfigured, "nil receiver Get must return ErrNotConfigured")
	assert.Nil(t, r.Names(), "nil receiver Names must return nil")
}

// --- Provider constructors ---

func TestNewGithub_RequiresBothCredentials(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		id     string
		secret string
	}{
		{"both empty", "", ""},
		{"id empty", "", "secret"},
		{"secret empty", "id", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewGithub(tc.id, tc.secret)
			require.ErrorIs(t, err, ErrNotConfigured)
		})
	}
}

func TestNewGithub_ValidCredentialsSucceed(t *testing.T) {
	t.Parallel()
	p, err := NewGithub("client-id", "client-secret")
	require.NoError(t, err)
	assert.Equal(t, "github", p.Name())
}

func TestNewSlack_RequiresBothCredentials(t *testing.T) {
	t.Parallel()
	_, err := NewSlack("", "secret")
	require.ErrorIs(t, err, ErrNotConfigured)
	_, err = NewSlack("id", "")
	require.ErrorIs(t, err, ErrNotConfigured)
}

func TestNewSlack_ValidCredentialsSucceed(t *testing.T) {
	t.Parallel()
	p, err := NewSlack("id", "secret")
	require.NoError(t, err)
	assert.Equal(t, "slack", p.Name())
}

func TestNewGoogleCalendar_RequiresBothCredentials(t *testing.T) {
	t.Parallel()
	_, err := NewGoogleCalendar("", "secret")
	require.ErrorIs(t, err, ErrNotConfigured)
}

func TestNewGoogleCalendar_ValidCredentialsSucceed(t *testing.T) {
	t.Parallel()
	p, err := NewGoogleCalendar("id", "secret")
	require.NoError(t, err)
	assert.Equal(t, "google_calendar", p.Name())
}

// --- AuthURL contract ---

func TestGithub_AuthURL_ContainsRequiredParams(t *testing.T) {
	t.Parallel()
	p, _ := NewGithub("my-id", "my-secret")
	url := p.AuthURL("state123", "https://example.com/callback")
	assert.Contains(t, url, "client_id=my-id")
	assert.Contains(t, url, "state=state123")
	assert.Contains(t, url, "redirect_uri=")
	assert.Contains(t, url, "scope=")
	assert.Contains(t, url, "allow_signup=false",
		"GitHub AuthURL must disable signup to prevent rogue account creation")
}

func TestSlack_AuthURL_UsesUserScopeNotBotScope(t *testing.T) {
	t.Parallel()
	p, _ := NewSlack("my-id", "my-secret")
	url := p.AuthURL("state456", "https://example.com/callback")
	assert.Contains(t, url, "user_scope=",
		"Slack must use user_scope (not scope) to request user-level tokens")
	assert.NotContains(t, url, "&scope=",
		"Slack personal integration must not request bot scopes")
}

func TestGoogle_AuthURL_RequestsOfflineAccessWithConsent(t *testing.T) {
	t.Parallel()
	p, _ := NewGoogleCalendar("my-id", "my-secret")
	url := p.AuthURL("state789", "https://example.com/callback")
	assert.Contains(t, url, "access_type=offline",
		"Google must request offline access to receive a refresh token")
	assert.Contains(t, url, "prompt=consent",
		"Google must prompt=consent to always issue a refresh token")
	assert.Contains(t, url, "response_type=code")
}

// --- Refresh contract ---

func TestGithub_Refresh_ReturnsErrRefreshNotSupported(t *testing.T) {
	t.Parallel()
	p, _ := NewGithub("id", "secret")
	_, err := p.Refresh(context.Background(), "any-token")
	require.ErrorIs(t, err, ErrRefreshNotSupported,
		"GitHub OAuth Apps do not issue refresh tokens")
}

func TestSlack_Refresh_ReturnsErrRefreshNotSupported(t *testing.T) {
	t.Parallel()
	p, _ := NewSlack("id", "secret")
	_, err := p.Refresh(context.Background(), "any-token")
	require.ErrorIs(t, err, ErrRefreshNotSupported,
		"Slack user tokens do not expire and have no refresh flow")
}

func TestGoogle_Refresh_EmptyTokenReturnsErrRefreshNotSupported(t *testing.T) {
	t.Parallel()
	p, _ := NewGoogleCalendar("id", "secret")
	_, err := p.Refresh(context.Background(), "")
	require.ErrorIs(t, err, ErrRefreshNotSupported,
		"empty refresh token must be treated as unsupported")
}

// --- Revoke contract ---

func TestGithub_Revoke_EmptyTokenIsNoop(t *testing.T) {
	t.Parallel()
	p, _ := NewGithub("id", "secret")
	err := p.Revoke(context.Background(), TokenSet{})
	require.NoError(t, err, "revoking empty tokens must succeed silently")
}

func TestSlack_Revoke_EmptyTokenIsNoop(t *testing.T) {
	t.Parallel()
	p, _ := NewSlack("id", "secret")
	err := p.Revoke(context.Background(), TokenSet{})
	require.NoError(t, err)
}

func TestGoogle_Revoke_EmptyTokenIsNoop(t *testing.T) {
	t.Parallel()
	p, _ := NewGoogleCalendar("id", "secret")
	err := p.Revoke(context.Background(), TokenSet{})
	require.NoError(t, err)
}

// --- Exchange with httptest ---

func TestGithub_Exchange_HappyPath(t *testing.T) {
	t.Parallel()
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			assert.Equal(t, "application/json", r.Header.Get("Accept"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"gho_abc","scope":"read:user,repo"}`))
		case "/user":
			assert.Contains(t, r.Header.Get("Authorization"), "Bearer gho_abc")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":42,"login":"octocat","email":"octo@example.com"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer tokenSrv.Close()

	p := &GithubProvider{
		clientID: "test-id", clientSecret: "test-secret",
		hc: tokenSrv.Client(),
	}
	// Patch URLs for the test server.
	origAuth, origToken, origUser := githubAuthorizeURL, githubTokenURL, githubUserURL
	defer func() {
		// These are package-level consts — can't patch. Use the
		// httptest client's Transport instead. Actually we need to
		// use the custom http.Client's base URL.
		_ = origAuth
		_ = origToken
		_ = origUser
	}()
	// We can't easily redirect const URLs, but the httptest.Client
	// follows the server URL. Let's use a custom Exchange that uses
	// the test server URL.
	tok, acc, err := exchangeViaTestServer(t, p, tokenSrv.URL, "test-code", "https://example.com/callback")
	require.NoError(t, err)
	assert.Equal(t, "gho_abc", tok.AccessToken)
	assert.Empty(t, tok.RefreshToken, "GitHub OAuth Apps must not return a refresh token")
	assert.Contains(t, tok.Scopes, "read:user")
	assert.Equal(t, "42", acc.ExternalID)
	assert.Equal(t, "octo@example.com", acc.Label,
		"when email is present, label should be the email")
}

func TestGithub_Exchange_FallsBackToLoginWhenNoEmail(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"gho_x","scope":"read:user"}`))
		case "/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":7,"login":"nomail","email":""}`))
		}
	}))
	defer srv.Close()

	p := &GithubProvider{clientID: "id", clientSecret: "secret", hc: srv.Client()}
	tok, acc, err := exchangeViaTestServer(t, p, srv.URL, "code", "https://x/cb")
	require.NoError(t, err)
	assert.Equal(t, "gho_x", tok.AccessToken)
	assert.Equal(t, "@nomail", acc.Label,
		"without email, label must be @login")
}

func TestSlack_Exchange_ExtractsUserToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ok": true,
			"authed_user": {"id":"U123","scope":"chat:write,im:history","access_token":"xoxp-user"},
			"team": {"name":"Acme"}
		}`))
	}))
	defer srv.Close()

	p := &SlackProvider{clientID: "id", clientSecret: "secret", hc: srv.Client()}
	tok, acc, err := slackExchangeViaTestServer(t, p, srv.URL, "code", "https://x/cb")
	require.NoError(t, err)
	assert.Equal(t, "xoxp-user", tok.AccessToken,
		"Slack must return the authed_user token, NOT the bot token")
	assert.Empty(t, tok.RefreshToken)
	assert.Equal(t, "U123", acc.ExternalID)
	assert.Equal(t, "Acme / U123", acc.Label,
		"label format: team_name / user_id")
}

func TestSlack_Exchange_RejectsNotOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_code"}`))
	}))
	defer srv.Close()

	p := &SlackProvider{clientID: "id", clientSecret: "secret", hc: srv.Client()}
	_, _, err := slackExchangeViaTestServer(t, p, srv.URL, "bad", "https://x/cb")
	require.Error(t, err, "Slack !ok response must be an error")
	assert.Contains(t, err.Error(), "invalid_code")
}

func TestGoogle_Exchange_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{
				"access_token":"ya29.xxx",
				"refresh_token":"1//rt",
				"expires_in":3600,
				"scope":"openid email https://www.googleapis.com/auth/calendar.readonly"
			}`))
		case "/oauth2/v3/userinfo":
			_, _ = w.Write([]byte(`{"sub":"112233","email":"user@gmail.com"}`))
		}
	}))
	defer srv.Close()

	p := &GoogleCalendarProvider{clientID: "id", clientSecret: "secret", hc: srv.Client()}
	tok, acc, err := googleExchangeViaTestServer(t, p, srv.URL, "code", "https://x/cb")
	require.NoError(t, err)
	assert.Equal(t, "ya29.xxx", tok.AccessToken)
	assert.Equal(t, "1//rt", tok.RefreshToken,
		"Google must surface the refresh token for background refresher")
	assert.False(t, tok.ExpiresAt.IsZero(),
		"Google tokens must carry an expiry")
	assert.Equal(t, "112233", acc.ExternalID)
	assert.Equal(t, "user@gmail.com", acc.Label)
}

func TestGoogle_Refresh_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		_ = r.ParseForm()                                           //#nosec G120 -- httptest receives small fixed test bodies; max-bytes guard is unnecessary
		assert.Equal(t, "refresh_token", r.FormValue("grant_type")) //#nosec G120 -- httptest receives small fixed test bodies
		assert.Equal(t, "old-rt", r.FormValue("refresh_token"))     //#nosec G120 -- httptest receives small fixed test bodies
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token":"ya29.new",
			"expires_in":3600,
			"scope":"openid email"
		}`))
	}))
	defer srv.Close()

	p := &GoogleCalendarProvider{clientID: "id", clientSecret: "secret", hc: srv.Client()}
	tok, err := googleRefreshViaTestServer(t, p, srv.URL, "old-rt")
	require.NoError(t, err)
	assert.Equal(t, "ya29.new", tok.AccessToken)
	assert.Equal(t, "old-rt", tok.RefreshToken,
		"when provider does not rotate, must preserve the original refresh token")
	assert.False(t, tok.ExpiresAt.IsZero())
}

func TestGoogle_Refresh_RotatedRefreshToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token":"ya29.new",
			"refresh_token":"1//rotated",
			"expires_in":3600,
			"scope":"openid"
		}`))
	}))
	defer srv.Close()

	p := &GoogleCalendarProvider{clientID: "id", clientSecret: "secret", hc: srv.Client()}
	tok, err := googleRefreshViaTestServer(t, p, srv.URL, "old-rt")
	require.NoError(t, err)
	assert.Equal(t, "1//rotated", tok.RefreshToken,
		"when provider issues a new refresh token, must adopt it")
}

// --- Revoke HTTP semantics ---

func TestGithub_Revoke_204IsSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/applications/")
		assert.Equal(t, http.MethodDelete, r.Method)
		// Verify Basic auth is used (client credentials).
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok, "revoke must use HTTP Basic auth")
		assert.Equal(t, "my-id", user)
		assert.Equal(t, "my-secret", pass)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := &GithubProvider{clientID: "my-id", clientSecret: "my-secret",
		hc: rewriteClient(srv.URL, map[string]string{"https://api.github.com": ""})}
	err := p.Revoke(context.Background(), TokenSet{AccessToken: "gho_tok"})
	require.NoError(t, err, "HTTP 204 means token was revoked successfully")
}

func TestGithub_Revoke_404IsSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := &GithubProvider{clientID: "id", clientSecret: "secret",
		hc: rewriteClient(srv.URL, map[string]string{"https://api.github.com": ""})}
	err := p.Revoke(context.Background(), TokenSet{AccessToken: "gho_tok"})
	require.NoError(t, err,
		"HTTP 404 means token is already gone — must be treated as success")
}

func TestGithub_Revoke_500IsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	p := &GithubProvider{clientID: "id", clientSecret: "secret",
		hc: rewriteClient(srv.URL, map[string]string{"https://api.github.com": ""})}
	err := p.Revoke(context.Background(), TokenSet{AccessToken: "gho_tok"})
	require.Error(t, err, "server error must propagate so caller can log it")
}

func TestSlack_Revoke_OkTrueIsSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer xoxp-tok", r.Header.Get("Authorization"),
			"Slack revoke must send the user token as Bearer")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	p := &SlackProvider{clientID: "id", clientSecret: "secret",
		hc: rewriteClient(srv.URL, map[string]string{"https://slack.com": ""})}
	err := p.Revoke(context.Background(), TokenSet{AccessToken: "xoxp-tok"})
	require.NoError(t, err)
}

func TestSlack_Revoke_NotAuthedIsSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"not_authed"}`))
	}))
	defer srv.Close()

	p := &SlackProvider{clientID: "id", clientSecret: "secret",
		hc: rewriteClient(srv.URL, map[string]string{"https://slack.com": ""})}
	err := p.Revoke(context.Background(), TokenSet{AccessToken: "xoxp-tok"})
	require.NoError(t, err,
		"not_authed means token is already invalid — treat as success")
}

func TestSlack_Revoke_TokenRevokedIsSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"token_revoked"}`))
	}))
	defer srv.Close()

	p := &SlackProvider{clientID: "id", clientSecret: "secret",
		hc: rewriteClient(srv.URL, map[string]string{"https://slack.com": ""})}
	err := p.Revoke(context.Background(), TokenSet{AccessToken: "xoxp-tok"})
	require.NoError(t, err,
		"token_revoked means already revoked — treat as success")
}

func TestSlack_Revoke_InvalidAuthIsSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	}))
	defer srv.Close()

	p := &SlackProvider{clientID: "id", clientSecret: "secret",
		hc: rewriteClient(srv.URL, map[string]string{"https://slack.com": ""})}
	err := p.Revoke(context.Background(), TokenSet{AccessToken: "xoxp-tok"})
	require.NoError(t, err,
		"invalid_auth means token is already unusable — treat as success")
}

func TestSlack_Revoke_UnknownErrorIsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"internal_error"}`))
	}))
	defer srv.Close()

	p := &SlackProvider{clientID: "id", clientSecret: "secret",
		hc: rewriteClient(srv.URL, map[string]string{"https://slack.com": ""})}
	err := p.Revoke(context.Background(), TokenSet{AccessToken: "xoxp-tok"})
	require.Error(t, err, "unknown Slack error must propagate")
	assert.Contains(t, err.Error(), "internal_error")
}

func TestGoogle_Revoke_200IsSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()                                   //#nosec G120 -- httptest receives small fixed test bodies; max-bytes guard is unnecessary
		assert.Equal(t, "my-refresh", r.FormValue("token"), //#nosec G120 -- httptest receives small fixed test bodies
			"Google revoke must prefer refresh token over access token")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &GoogleCalendarProvider{clientID: "id", clientSecret: "secret",
		hc: rewriteClient(srv.URL, map[string]string{"https://oauth2.googleapis.com": ""})}
	err := p.Revoke(context.Background(), TokenSet{
		AccessToken: "access", RefreshToken: "my-refresh",
	})
	require.NoError(t, err)
}

func TestGoogle_Revoke_FallsBackToAccessToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()                                    //#nosec G120 -- httptest receives small fixed test bodies; max-bytes guard is unnecessary
		assert.Equal(t, "access-only", r.FormValue("token"), //#nosec G120 -- httptest receives small fixed test bodies
			"must use access token when no refresh token exists")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &GoogleCalendarProvider{clientID: "id", clientSecret: "secret",
		hc: rewriteClient(srv.URL, map[string]string{"https://oauth2.googleapis.com": ""})}
	err := p.Revoke(context.Background(), TokenSet{AccessToken: "access-only"})
	require.NoError(t, err)
}

func TestGoogle_Revoke_InvalidTokenIsSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer srv.Close()

	p := &GoogleCalendarProvider{clientID: "id", clientSecret: "secret",
		hc: rewriteClient(srv.URL, map[string]string{"https://oauth2.googleapis.com": ""})}
	err := p.Revoke(context.Background(), TokenSet{AccessToken: "already-gone"})
	require.NoError(t, err,
		"Google 400 + invalid_token means token is already revoked — treat as success")
}

func TestGoogle_Revoke_OtherErrorIsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer srv.Close()

	p := &GoogleCalendarProvider{clientID: "id", clientSecret: "secret",
		hc: rewriteClient(srv.URL, map[string]string{"https://oauth2.googleapis.com": ""})}
	err := p.Revoke(context.Background(), TokenSet{RefreshToken: "rt"})
	require.Error(t, err, "non-success/non-invalid_token responses must propagate")
}

// --- Sentinel errors ---

func TestErrNotConfigured_IsDistinct(t *testing.T) {
	t.Parallel()
	assert.False(t, errors.Is(ErrNotConfigured, ErrRefreshNotSupported))
	assert.False(t, errors.Is(ErrRefreshNotSupported, ErrNotConfigured))
}

// --- test helpers ---

// stubProvider is a minimal Provider for Registry tests.
type stubProvider struct {
	name string
}

func (s *stubProvider) Name() string               { return s.name }
func (s *stubProvider) AuthURL(_, _ string) string { return "" }
func (s *stubProvider) Exchange(_ context.Context, _, _ string) (*TokenSet, *Account, error) {
	return nil, nil, nil
}
func (s *stubProvider) Refresh(_ context.Context, _ string) (*TokenSet, error) {
	return nil, ErrRefreshNotSupported
}
func (s *stubProvider) Revoke(_ context.Context, _ TokenSet) error { return nil }

// exchangeViaTestServer calls the GithubProvider Exchange method by
// temporarily pointing its http.Client at the test server. Because
// the target URLs are hard-coded consts, we POST via the test
// server's URL by constructing requests manually.
func exchangeViaTestServer(t *testing.T, p *GithubProvider, baseURL, code, redirectURI string) (*TokenSet, *Account, error) {
	t.Helper()
	// Replace the embedded http.Client with one whose Transport
	// rewrites all github.com requests to the test server.
	p.hc = rewriteClient(baseURL, map[string]string{
		"https://github.com":     "",
		"https://api.github.com": "",
	})
	return p.Exchange(context.Background(), code, redirectURI)
}

func slackExchangeViaTestServer(t *testing.T, p *SlackProvider, baseURL, code, redirectURI string) (*TokenSet, *Account, error) {
	t.Helper()
	p.hc = rewriteClient(baseURL, map[string]string{
		"https://slack.com": "",
	})
	return p.Exchange(context.Background(), code, redirectURI)
}

func googleExchangeViaTestServer(t *testing.T, p *GoogleCalendarProvider, baseURL, code, redirectURI string) (*TokenSet, *Account, error) {
	t.Helper()
	p.hc = rewriteClient(baseURL, map[string]string{
		"https://oauth2.googleapis.com": "",
		"https://www.googleapis.com":    "",
		"https://accounts.google.com":   "",
	})
	return p.Exchange(context.Background(), code, redirectURI)
}

func googleRefreshViaTestServer(t *testing.T, p *GoogleCalendarProvider, baseURL, refreshToken string) (*TokenSet, error) {
	t.Helper()
	p.hc = rewriteClient(baseURL, map[string]string{
		"https://oauth2.googleapis.com": "",
	})
	return p.Refresh(context.Background(), refreshToken)
}

// rewriteClient returns an *http.Client whose Transport rewrites
// requests from the origin prefixes to the test server base URL,
// preserving the path and query.
func rewriteClient(testBase string, origins map[string]string) *http.Client {
	return &http.Client{
		Transport: &rewriteTransport{base: testBase, origins: origins},
	}
}

type rewriteTransport struct {
	base    string
	origins map[string]string
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	original := req.URL.String()
	for origin := range rt.origins {
		if len(original) >= len(origin) && original[:len(origin)] == origin {
			newURL := rt.base + original[len(origin):]
			parsed, err := http.NewRequest(req.Method, newURL, req.Body) //#nosec G107 G704 -- newURL is rebuilt against the test server origin, not user input
			if err != nil {
				return nil, err
			}
			parsed.Header = req.Header
			return http.DefaultTransport.RoundTrip(parsed)
		}
	}
	return http.DefaultTransport.RoundTrip(req)
}
