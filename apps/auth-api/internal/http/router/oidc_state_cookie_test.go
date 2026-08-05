package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
)

// devOrigin is the plain-http origin local development runs on. The
// state cookie has to survive it: a Secure cookie is silently dropped
// by the browser over http, which would leave every dev sign-in with no
// verifier to send back.
const devOrigin = "http://localhost:8082"

// stopAtClaimStore fails the single-use claim so a callback stops one
// step past the cookie check. That makes "the cookie was accepted" an
// observable outcome without the handler reaching out to the real
// identity provider.
type stopAtClaimStore struct {
	mu    sync.Mutex
	calls int
}

func (s *stopAtClaimStore) Consume(context.Context, string, time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return false, errors.New("stop after the cookie check")
}

func (s *stopAtClaimStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// oidcCookieDeps builds a router with the GitHub provider configured.
// GitHub's authorization URL is fixed, so /start needs no network.
func oidcCookieDeps(t *testing.T) Deps {
	t.Helper()
	deps := stubDeps(t)
	deps.OIDCGithub = auth.NewGithubOAuth(auth.GithubOAuthConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  devOrigin + "/auth/oidc/github/callback",
	})
	return deps
}

// startGithubOIDC drives GET /auth/oidc/github/start and returns the
// state parameter from the authorization URL along with the verifier
// cookie the response set.
func startGithubOIDC(t *testing.T, h http.Handler) (string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/github/start", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equalf(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var body struct {
		AuthorizationURL string `json:"authorizationUrl"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	parsed, err := url.Parse(body.AuthorizationURL)
	require.NoError(t, err)
	state := parsed.Query().Get("state")
	require.NotEmpty(t, state, "the authorization URL must carry a state parameter")

	var verifier *http.Cookie
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == "nd_oidc" {
			verifier = c
		}
	}
	return state, verifier
}

// TestOIDCStartSetsStateCookieOverTheWire proves the binding survives
// the HTTP layer: /start must emit a Set-Cookie header, otherwise the
// browser has nothing to send back and every sign-in would break — or,
// worse, the callback would have to accept states with no cookie.
func TestOIDCStartSetsStateCookieOverTheWire(t *testing.T) {
	t.Parallel()
	h := Build(oidcCookieDeps(t))

	state, verifier := startGithubOIDC(t, h)
	require.NotNil(t, verifier, "/start must set the nd_oidc verifier cookie")
	assert.NotEmpty(t, verifier.Value)
	assert.True(t, verifier.HttpOnly, "the verifier must be unreadable from JS")
	assert.Equal(t, "/auth/oidc", verifier.Path, "the cookie must not ride along with other requests")
	assert.Positive(t, verifier.MaxAge)
	assert.NotContains(t, state, verifier.Value,
		"the verifier must not be recoverable from the URL parameter")
}

// TestOIDCCallbackWithoutCookieIsRejectedOverTheWire is the end-to-end
// login-CSRF check: a state minted in one browser, replayed from another
// that carries no verifier cookie, must be refused with
// AUTH.OIDC.STATE_MISMATCH rather than starting a code exchange.
func TestOIDCCallbackWithoutCookieIsRejectedOverTheWire(t *testing.T) {
	t.Parallel()
	h := Build(oidcCookieDeps(t))

	state, verifier := startGithubOIDC(t, h)
	require.NotNil(t, verifier)

	req := httptest.NewRequest(http.MethodGet,
		"/auth/oidc/github/callback?code=attacker-code&state="+url.QueryEscape(state), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, apierrors.AuthOidcStateMismatch.Status, rec.Code)
	assert.Contains(t, rec.Body.String(), apierrors.AuthOidcStateMismatch.Code)
}

// TestOIDCStateCookieSurvivesPlainHTTPDev is the local-development
// guard. Development serves auth-api over http, and a Secure cookie is
// dropped by the browser on a plain-http response: every sign-in would
// then arrive at the callback with no verifier and be refused, i.e. OIDC
// would be broken in dev only. The cookie must therefore come back
// non-Secure with SameSite=Lax, which is still sent on the top-level
// redirect the identity provider performs.
func TestOIDCStateCookieSurvivesPlainHTTPDev(t *testing.T) {
	t.Parallel()
	deps := oidcCookieDeps(t)
	deps.CookieSecure = false
	h := Build(deps)

	_, verifier := startGithubOIDC(t, h)
	require.NotNil(t, verifier)
	assert.False(t, verifier.Secure, "a Secure cookie is never stored over http")
	assert.Equal(t, http.SameSiteLaxMode, verifier.SameSite,
		"Lax is the only mode a non-Secure cookie may use, and it still rides the IdP redirect")

	// The Secure rule above is asserted on the attribute itself because
	// net/http/cookiejar does not model it. What the jar does model is
	// domain and path matching, which is the other way this cookie could
	// silently fail to come back: Path=/auth/oidc has to cover the
	// callback route.
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	startURL, err := url.Parse(devOrigin + "/auth/oidc/github/start")
	require.NoError(t, err)
	jar.SetCookies(startURL, []*http.Cookie{verifier})

	callbackURL, err := url.Parse(devOrigin + "/auth/oidc/github/callback")
	require.NoError(t, err)
	sent := jar.Cookies(callbackURL)
	require.Len(t, sent, 1, "the dev browser must send the verifier back to the callback")
	assert.Equal(t, "nd_oidc", sent[0].Name)
	assert.Equal(t, verifier.Value, sent[0].Value)
}

// TestOIDCStateCookieIsCrossSiteCapableInProd covers the other half of
// the derivation: over https the cookie is written by a cross-site fetch
// from accounts-web, so it needs SameSite=None, which the spec only
// permits together with Secure.
func TestOIDCStateCookieIsCrossSiteCapableInProd(t *testing.T) {
	t.Parallel()
	deps := oidcCookieDeps(t)
	deps.CookieSecure = true
	h := Build(deps)

	_, verifier := startGithubOIDC(t, h)
	require.NotNil(t, verifier)
	assert.True(t, verifier.Secure)
	assert.Equal(t, http.SameSiteNoneMode, verifier.SameSite,
		"SameSite=None is what lets accounts-web on another origin receive the cookie")

	// The two attributes are coupled: a browser rejects SameSite=None
	// without Secure outright, so this pairing is the only valid shape
	// for the https deployment — and the reason the dev build must take
	// the other branch instead of simply dropping Secure here.
	assert.Truef(t, verifier.Secure || verifier.SameSite != http.SameSiteNoneMode,
		"SameSite=None requires Secure; got Secure=%v SameSite=%v", verifier.Secure, verifier.SameSite)
}

// TestOIDCCallbackAcceptsTheCookieTheDevBrowserStored closes the loop:
// the exact cookie a plain-http browser kept from /start is accepted by
// the callback, which is only observable here because the single-use
// claim is rigged to fail immediately after the cookie check.
func TestOIDCCallbackAcceptsTheCookieTheDevBrowserStored(t *testing.T) {
	t.Parallel()
	store := &stopAtClaimStore{}
	deps := oidcCookieDeps(t)
	deps.CookieSecure = false
	deps.SingleUse = store
	h := Build(deps)

	state, verifier := startGithubOIDC(t, h)
	require.NotNil(t, verifier)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	startURL, err := url.Parse(devOrigin + "/auth/oidc/github/start")
	require.NoError(t, err)
	jar.SetCookies(startURL, []*http.Cookie{verifier})
	callbackURL, err := url.Parse(devOrigin + "/auth/oidc/github/callback?code=c&state=" + url.QueryEscape(state))
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, callbackURL.String(), nil)
	for _, c := range jar.Cookies(callbackURL) {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.NotContains(t, rec.Body.String(), apierrors.AuthOidcStateMismatch.Code,
		"the cookie a dev browser stored must satisfy the state binding")
	assert.Equal(t, 1, store.count(), "the callback must have reached the single-use claim")

	// Without the cookie the same request is refused before the claim,
	// which is what makes the assertion above meaningful.
	bare := httptest.NewRequest(http.MethodGet, callbackURL.String(), nil)
	bareRec := httptest.NewRecorder()
	h.ServeHTTP(bareRec, bare)
	assert.Contains(t, bareRec.Body.String(), apierrors.AuthOidcStateMismatch.Code)
	assert.Equal(t, 1, store.count(), "a cookie-less callback must not reach the claim")
}
