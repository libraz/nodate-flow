package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	internauth "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// oidcProvider bundles everything needed to drive one provider's
// start/callback pair through the shared state gate. Every OIDC provider
// must appear here: the binding is only worth anything if no provider is
// left on the old unbound path.
type oidcProvider struct {
	name string
	// newDeps returns deps wired with a fake exchanger that would
	// succeed far enough to prove the state gate let the request past.
	newDeps func(t *testing.T) Deps
	// start runs the provider's /start handler.
	start func(deps Deps) (*OIDCStartOutput, error)
	// callback runs the provider's callback handler.
	callback func(deps Deps, in *OIDCCallbackInput) error
}

func oidcProviders() []oidcProvider {
	return []oidcProvider{
		{
			name: "google",
			newDeps: func(t *testing.T) Deps {
				t.Helper()
				// The Google client is a concrete type that fetches a
				// live discovery document on first use, so Google is
				// only driven through the paths that end at the state
				// gate. It must be non-nil or the handler bails out on
				// the not-configured check before reaching that gate.
				return Deps{
					JWT:   testJWTIssuer(t),
					OIDC:  internauth.NewOIDCClient(internauth.OIDCConfig{}),
					Audit: audit.NoopSink{},
				}
			},
			start: func(deps Deps) (*OIDCStartOutput, error) {
				return OIDCGoogleStart(deps)(context.Background(), &struct{}{})
			},
			callback: func(deps Deps, in *OIDCCallbackInput) error {
				_, err := OIDCGoogleCallback(deps)(context.Background(), in)
				return err
			},
		},
		{
			name: "github",
			newDeps: func(t *testing.T) Deps {
				t.Helper()
				gh := &fakeGithubExchanger{err: errors.New("stop after the state gate")}
				return Deps{JWT: testJWTIssuer(t), OIDCGithub: gh, Audit: audit.NoopSink{}}
			},
			start: func(deps Deps) (*OIDCStartOutput, error) {
				return OIDCGithubStart(deps)(context.Background(), &struct{}{})
			},
			callback: func(deps Deps, in *OIDCCallbackInput) error {
				_, err := OIDCGithubCallback(deps)(context.Background(), in)
				return err
			},
		},
		{
			name: "microsoft",
			newDeps: func(t *testing.T) Deps {
				t.Helper()
				ms := &fakeMicrosoftExchanger{err: errors.New("stop after the state gate")}
				return Deps{
					JWT:                       testJWTIssuer(t),
					OIDCMicrosoft:             ms,
					MicrosoftAllowedTenantIDs: []string{testMicrosoftTenantID},
					Audit:                     audit.NoopSink{},
				}
			},
			start: func(deps Deps) (*OIDCStartOutput, error) {
				return OIDCMicrosoftStart(deps)(context.Background(), &struct{}{})
			},
			callback: func(deps Deps, in *OIDCCallbackInput) error {
				_, err := OIDCMicrosoftCallback(deps)(context.Background(), in)
				return err
			},
		},
	}
}

func testJWTIssuer(t *testing.T) *internauth.JWTIssuer {
	t.Helper()
	issuer, err := internauth.NewJWTIssuer(nil, "iss", "aud", time.Minute)
	require.NoError(t, err)
	return issuer
}

// exchangeReached reports whether the provider's fake exchanger was
// called. A rejected state must never get that far.
func (p oidcProvider) exchangeReached(deps Deps) bool {
	switch {
	case deps.OIDCGithub != nil:
		gh, ok := deps.OIDCGithub.(*fakeGithubExchanger)
		return ok && gh.gotCode != ""
	case deps.OIDCMicrosoft != nil:
		ms, ok := deps.OIDCMicrosoft.(*fakeMicrosoftExchanger)
		return ok && ms.gotCode != ""
	default:
		return false
	}
}

func requireStateMismatch(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem), "expected handlerutil.ProblemDetails, got %T", err)
	assert.Equal(t, apierrors.AuthOidcStateMismatch.Code, problem.Type)
}

// TestOIDCStart_IssuesVerifierCookie proves every provider's /start now
// hands the browser a cookie. Without it the callback has nothing to
// compare against and the flow is open to login CSRF.
func TestOIDCStart_IssuesVerifierCookie(t *testing.T) {
	t.Parallel()
	for _, p := range oidcProviders() {
		if p.name == "google" {
			// Google's start needs a live discovery document to build
			// the authorization URL; its cookie is covered by the
			// callback-side tests below.
			continue
		}
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			deps := p.newDeps(t)
			deps.SingleUse = authn.NewMemorySingleUseStore()

			out, err := p.start(deps)
			require.NoError(t, err)
			require.NotEmpty(t, out.Body.State)
			assert.Equal(t, oidcStateCookieName, out.SetCookie.Name)
			assert.NotEmpty(t, out.SetCookie.Value, "the state must be bound to a browser cookie")
			assert.True(t, out.SetCookie.HttpOnly, "the verifier must not be readable from JS")
			assert.Positive(t, out.SetCookie.MaxAge)

			// The cookie the browser got must be the one that unlocks
			// the state, and nothing else.
			_, verr := deps.JWT.VerifyOIDCStateBinding(out.Body.State, out.SetCookie.Value, p.name)
			require.NoError(t, verr)
			_, verr = deps.JWT.VerifyOIDCStateBinding(out.Body.State, "some-other-verifier", p.name)
			require.Error(t, verr, "a foreign verifier must not unlock the state")
		})
	}
}

// TestOIDCCallback_RejectsStateWithoutCookie is the login-CSRF
// regression: an attacker mints a state from their own browser, pairs it
// with their own authorization code, and gets a victim to open the
// callback URL. The victim's browser carries no verifier cookie, so the
// callback must refuse instead of signing them into the attacker's
// account.
func TestOIDCCallback_RejectsStateWithoutCookie(t *testing.T) {
	t.Parallel()
	for _, p := range oidcProviders() {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			attacker := p.newDeps(t)
			attacker.SingleUse = authn.NewMemorySingleUseStore()
			state, _ := boundOIDCState(t, &attacker, p.name, "nonce-value")

			victim := p.newDeps(t)
			victim.JWT = attacker.JWT // same server, different browser
			victim.SingleUse = attacker.SingleUse

			err := p.callback(victim, &OIDCCallbackInput{Code: "attacker-code", State: state})
			requireStateMismatch(t, err)
			assert.False(t, p.exchangeReached(victim),
				"the code must never be exchanged for a state with no cookie behind it")
		})
	}
}

// TestOIDCCallback_RejectsForeignCookie covers the same attack with a
// cookie present but belonging to a different flow: the victim started
// their own sign-in, so a cookie exists, yet it does not match the state
// the attacker supplied.
func TestOIDCCallback_RejectsForeignCookie(t *testing.T) {
	t.Parallel()
	for _, p := range oidcProviders() {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			deps := p.newDeps(t)
			deps.SingleUse = authn.NewMemorySingleUseStore()

			attackerState, _ := boundOIDCState(t, &deps, p.name, "nonce-value")
			_, victimCookie := boundOIDCState(t, &deps, p.name, "nonce-value")

			err := p.callback(deps, &OIDCCallbackInput{
				Code:        "attacker-code",
				State:       attackerState,
				StateCookie: victimCookie,
			})
			requireStateMismatch(t, err)
			assert.False(t, p.exchangeReached(deps),
				"a state and a cookie from different flows must not be accepted together")
		})
	}
}

// TestOIDCCallback_StateIsSingleUse proves a state cannot be redeemed
// twice inside its ten-minute lifetime, even from the browser that owns
// the cookie. Deleting the cookie on the way out is not enough on its
// own: two tabs share one cookie jar, so the claim has to be recorded
// server-side.
func TestOIDCCallback_StateIsSingleUse(t *testing.T) {
	t.Parallel()
	for _, p := range oidcProviders() {
		if p.name == "google" {
			// Google's exchange needs a live provider, so the first call
			// cannot get past it here. The other two providers cover the
			// shared gate, which is the same code for all three.
			continue
		}
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			deps := p.newDeps(t)
			deps.SingleUse = authn.NewMemorySingleUseStore()
			state, cookie := boundOIDCState(t, &deps, p.name, "nonce-value")

			in := &OIDCCallbackInput{Code: "auth-code", State: state, StateCookie: cookie}
			// First redemption gets past the gate and fails at the fake
			// exchanger, which is how we know the gate let it through.
			require.Error(t, p.callback(deps, in), "the fake exchanger always fails")
			require.True(t, p.exchangeReached(deps), "the first use must reach the exchange")

			second := p.newDeps(t)
			second.JWT = deps.JWT
			second.SingleUse = deps.SingleUse
			err := p.callback(second, &OIDCCallbackInput{
				Code:        "auth-code",
				State:       state,
				StateCookie: cookie,
			})
			requireStateMismatch(t, err)
			assert.False(t, p.exchangeReached(second),
				"a replayed state must be refused before the exchange")
		})
	}
}

// TestOIDCCallback_HappyPathPassesTheGate keeps the guard honest: a
// state and cookie minted together, used once, must still reach the
// token exchange.
func TestOIDCCallback_HappyPathPassesTheGate(t *testing.T) {
	t.Parallel()
	for _, p := range oidcProviders() {
		if p.name == "google" {
			continue
		}
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			deps := p.newDeps(t)
			deps.SingleUse = authn.NewMemorySingleUseStore()
			state, cookie := boundOIDCState(t, &deps, p.name, "nonce-value")

			require.Error(t, p.callback(deps, &OIDCCallbackInput{
				Code:        "auth-code",
				State:       state,
				StateCookie: cookie,
			}), "the fake exchanger always fails")
			assert.True(t, p.exchangeReached(deps),
				"a properly bound state must reach the token exchange")
		})
	}
}

// TestOIDCCallback_RejectsCrossProviderState keeps the provider binding
// wired to the same gate: a state minted for one callback must not be
// redeemable at another, cookie or no cookie.
func TestOIDCCallback_RejectsCrossProviderState(t *testing.T) {
	t.Parallel()
	deps := Deps{
		JWT:                       testJWTIssuer(t),
		OIDCGithub:                &fakeGithubExchanger{err: errors.New("unreached")},
		OIDCMicrosoft:             &fakeMicrosoftExchanger{err: errors.New("unreached")},
		MicrosoftAllowedTenantIDs: []string{testMicrosoftTenantID},
		Audit:                     audit.NoopSink{},
		SingleUse:                 authn.NewMemorySingleUseStore(),
	}
	state, cookie := boundOIDCState(t, &deps, "github", "nonce-value")

	_, err := OIDCMicrosoftCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       state,
		StateCookie: cookie,
	})
	requireStateMismatch(t, err)
}

// TestOIDCCallback_RejectsUnboundState covers a state minted by the
// legacy constructor, which carries no cookie hash. Accepting it would
// reopen the hole for anything still calling the old path.
func TestOIDCCallback_RejectsUnboundState(t *testing.T) {
	t.Parallel()
	gh := &fakeGithubExchanger{err: errors.New("unreached")}
	deps := Deps{
		JWT:        testJWTIssuer(t),
		OIDCGithub: gh,
		Audit:      audit.NoopSink{},
		SingleUse:  authn.NewMemorySingleUseStore(),
	}
	unbound, err := deps.JWT.SignOIDCStateForProvider("nonce-value", "github")
	require.NoError(t, err)

	_, err = OIDCGithubCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       unbound,
		StateCookie: http.Cookie{Name: oidcStateCookieName, Value: "anything"},
	})
	requireStateMismatch(t, err)
	assert.Empty(t, gh.gotCode, "an unbound state must not reach the exchange")
}

// TestOIDCCallback_FailsClosedWithoutSingleUseStore asserts a missing
// store is a wiring error rather than a silent downgrade to a replayable
// state.
func TestOIDCCallback_FailsClosedWithoutSingleUseStore(t *testing.T) {
	t.Parallel()
	gh := &fakeGithubExchanger{err: errors.New("unreached")}
	deps := Deps{
		JWT:        testJWTIssuer(t),
		OIDCGithub: gh,
		Audit:      audit.NoopSink{},
	}
	binding, err := deps.JWT.NewOIDCStateBinding("nonce-value", "github")
	require.NoError(t, err)

	_, err = OIDCGithubCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       binding.State,
		StateCookie: oidcVerifierCookie(binding.CookieValue),
	})
	require.Error(t, err)
	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(err, &problem))
	assert.Equal(t, apierrors.InternalUnexpected.Code, problem.Type)
	assert.Empty(t, gh.gotCode, "no exchange without a working replay guard")
}

// TestOIDCCallback_ClearsVerifierCookie asserts the browser is not left
// holding a spent verifier once the flow ends.
func TestOIDCCallback_ClearsVerifierCookie(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true
	deps.OIDCGithub = &fakeGithubExchanger{
		claims: &internauth.GithubClaims{
			Sub:   "gh-cookie-clear-7007",
			Login: "frank",
			Name:  "Frank",
			Email: "oidc-cookie-clear-" + authn.RandomHex(8) + "@example.test",
		},
	}

	state, cookie := signedGithubState(t, &deps)
	out, err := OIDCGithubCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       state,
		StateCookie: cookie,
	})
	require.NoError(t, err)

	var cleared *http.Cookie
	for i := range out.SetCookie {
		if out.SetCookie[i].Name == oidcStateCookieName {
			cleared = &out.SetCookie[i]
		}
	}
	require.NotNil(t, cleared, "the callback must evict the state cookie")
	assert.Empty(t, cleared.Value)
	assert.Negative(t, cleared.MaxAge, "an eviction sets Max-Age=0 on the wire")
	assert.NotEmpty(t, refreshCookieFrom(out.SetCookie).Value,
		"the refresh cookie must still be issued alongside the eviction")
}
