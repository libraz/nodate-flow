package auth

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internauth "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// countUsersByEmail returns how many users rows hold the given email,
// so tests can prove account linking did NOT create a duplicate user
// (which would have tripped the uniq_users_email constraint and surfaced
// the original opaque 500).
func countUsersByEmail(t *testing.T, db *sql.DB, email string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM users WHERE email = ?", email).Scan(&n))
	return n
}

// findIdentityUserID returns the user_id bound to (provider, subject), or
// (0, false) when no identity row exists.
func findIdentityUserID(t *testing.T, db *sql.DB, provider, subject string) (uint32, bool) {
	t.Helper()
	var uid uint32
	err := db.QueryRowContext(context.Background(),
		"SELECT user_id FROM identities WHERE provider = ? AND subject = ? AND enabled = TRUE",
		provider, subject).Scan(&uid)
	if err == sql.ErrNoRows {
		return 0, false
	}
	require.NoError(t, err)
	return uid, true
}

// boundOIDCState puts deps in the state a real /start call would leave
// behind and returns what the browser carries back: the state parameter
// in the URL plus the verifier cookie. Both halves are required at the
// callback, so tests that skip either one exercise the rejection path.
func boundOIDCState(t *testing.T, deps *Deps, provider, nonce string) (string, http.Cookie) {
	t.Helper()
	if deps.SingleUse == nil {
		deps.SingleUse = authn.NewMemorySingleUseStore()
	}
	binding, err := deps.JWT.NewOIDCStateBinding(nonce, provider)
	require.NoError(t, err)
	return binding.State, oidcVerifierCookie(binding.CookieValue)
}

// oidcVerifierCookie wraps a verifier the way Huma hands a request
// cookie to the handler.
func oidcVerifierCookie(value string) http.Cookie {
	return http.Cookie{Name: oidcStateCookieName, Value: value}
}

// refreshCookieFrom picks the refresh cookie out of a callback's
// Set-Cookie list. The list also carries the eviction of the state
// verifier, so tests must not assume a single entry.
func refreshCookieFrom(cookies []http.Cookie) http.Cookie {
	for _, c := range cookies {
		if c.Name == refreshCookieName {
			return c
		}
	}
	return http.Cookie{}
}

// signedGithubState mints a bound state + cookie pair for GitHub.
func signedGithubState(t *testing.T, deps *Deps) (string, http.Cookie) {
	t.Helper()
	return boundOIDCState(t, deps, "github", "nonce-value")
}

// signedMicrosoftState mints a bound state + cookie pair for Microsoft.
func signedMicrosoftState(t *testing.T, deps *Deps) (string, http.Cookie) {
	t.Helper()
	return boundOIDCState(t, deps, "microsoft", "nonce-value")
}

// TestOIDCGithubCallback_LinksExistingPasswordAccount proves the fix for
// the M-7 / P2-6 defect: a user who already holds a local-password
// account signing in via GitHub with the SAME verified email links the
// GitHub identity onto the existing user and logs in, instead of hitting
// the uniq_users_email constraint and surfacing an opaque 500.
func TestOIDCGithubCallback_LinksExistingPasswordAccount(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true

	// A pre-existing local-password account.
	existingUID, existingPub, email := b2NewUser(t, deps.Queries)

	const ghSub = "gh-link-subject-1001"
	deps.OIDCGithub = &fakeGithubExchanger{
		claims: &internauth.GithubClaims{
			Sub:   ghSub,
			Login: "alice",
			Name:  "Alice",
			Email: email,
		},
	}

	state, stateCookie := signedGithubState(t, &deps)
	out, err := OIDCGithubCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       state,
		StateCookie: stateCookie,
	})
	require.NoError(t, err, "same-email GitHub sign-in must link, not 500")
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Body.AccessToken, "linked sign-in must issue an access token")
	assert.NotEmpty(t, refreshCookieFrom(out.SetCookie).Value, "linked sign-in must set a refresh cookie")

	// The GitHub identity must now point at the SAME pre-existing user.
	linkedUID, ok := findIdentityUserID(t, db, "github", ghSub)
	require.True(t, ok, "a github identity row must have been created")
	assert.Equal(t, existingUID, linkedUID, "github identity must link onto the existing user")

	// No duplicate user was created for the shared email.
	assert.Equal(t, 1, countUsersByEmail(t, db, email),
		"linking must not create a second user for the same email")

	// The local-password identity is untouched and the user still
	// resolves to its original public id.
	pub, perr := deps.Queries.FindUserPublicIdById(context.Background(), existingUID)
	require.NoError(t, perr)
	assert.Equal(t, existingPub.String(), pub.String())
}

// TestOIDCMicrosoftCallback_DoesNotAutoLinkExistingPasswordAccount proves
// Microsoft sign-in does not bind a new tenant subject onto a same-email
// local account by email alone. The account can still sign in after an
// explicit Microsoft identity exists, but first contact must not silently
// link across tenants.
func TestOIDCMicrosoftCallback_DoesNotAutoLinkExistingPasswordAccount(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true
	deps.MicrosoftAllowedTenantIDs = []string{testMicrosoftTenantID}

	_, _, email := b2NewUser(t, deps.Queries)

	const msSub = "ms-link-subject-2002"
	deps.OIDCMicrosoft = &fakeMicrosoftExchanger{
		claims: &internauth.MicrosoftClaims{
			Sub:                      msSub,
			Email:                    email,
			Name:                     "Alice",
			TenantID:                 testMicrosoftTenantID,
			EmailVerified:            true,
			EmailDomainOwnerVerified: true,
		},
	}

	state, stateCookie := signedMicrosoftState(t, &deps)
	out, err := OIDCMicrosoftCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       state,
		StateCookie: stateCookie,
	})
	require.Error(t, err, "same-email Microsoft sign-in must not auto-link")
	assert.Nil(t, out)

	linkedUID, ok := findIdentityUserID(t, db, "microsoft", msSub)
	assert.False(t, ok, "a microsoft identity row must not be created by email-only linking")
	assert.Zero(t, linkedUID)
	assert.Equal(t, 1, countUsersByEmail(t, db, email))
}

// TestOIDCLink_LinkingNotGatedByRegistrationOpen proves that linking an
// OIDC provider onto an already-existing account is treated as login, not
// registration: it succeeds even when self-service registration is closed.
// Only the brand-new-user path is gated by RegistrationOpen.
func TestOIDCLink_LinkingNotGatedByRegistrationOpen(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = false // registration closed

	existingUID, _, email := b2NewUser(t, deps.Queries)

	const ghSub = "gh-link-subject-3003"
	deps.OIDCGithub = &fakeGithubExchanger{
		claims: &internauth.GithubClaims{
			Sub:   ghSub,
			Login: "bob",
			Name:  "Bob",
			Email: email,
		},
	}

	state, stateCookie := signedGithubState(t, &deps)
	out, err := OIDCGithubCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       state,
		StateCookie: stateCookie,
	})
	require.NoError(t, err, "linking onto an existing account is login, not registration")
	assert.NotEmpty(t, out.Body.AccessToken)

	linkedUID, ok := findIdentityUserID(t, db, "github", ghSub)
	require.True(t, ok)
	assert.Equal(t, existingUID, linkedUID)
}

// TestOIDCGithubCallback_BrandNewEmailStillRegisters proves the
// non-linking path is preserved: a GitHub sign-in for an email no
// existing user holds still auto-provisions a fresh user + identity when
// registration is open.
func TestOIDCGithubCallback_BrandNewEmailStillRegisters(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true

	// An email no user currently holds.
	newEmail := "oidc-new-" + types.New().String() + "@example.test"
	require.Equal(t, 0, countUsersByEmail(t, db, newEmail))

	const ghSub = "gh-new-subject-4004"
	deps.OIDCGithub = &fakeGithubExchanger{
		claims: &internauth.GithubClaims{
			Sub:   ghSub,
			Login: "carol",
			Name:  "Carol",
			Email: newEmail,
		},
	}

	state, stateCookie := signedGithubState(t, &deps)
	out, err := OIDCGithubCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       state,
		StateCookie: stateCookie,
	})
	require.NoError(t, err, "brand-new email must register")
	assert.NotEmpty(t, out.Body.AccessToken)

	// A fresh user now holds the email, bound to the github identity.
	assert.Equal(t, 1, countUsersByEmail(t, db, newEmail),
		"brand-new email must provision exactly one user")
	provisionedUID, ok := findIdentityUserID(t, db, "github", ghSub)
	require.True(t, ok, "a github identity row must have been created for the new user")

	var emailOfUser string
	require.NoError(t, db.QueryRowContext(context.Background(),
		"SELECT email FROM users WHERE id = ?", provisionedUID).Scan(&emailOfUser))
	assert.Equal(t, newEmail, emailOfUser)
}

// TestOIDCGithubCallback_ProvisionsUsableTimezone proves an OIDC-created
// account is seeded with a real IANA zone. An OIDC callback carries no
// timezone claim, so the insert has to supply the fallback itself; a
// params struct that leaves the field unset binds the zero value and
// stores an empty string over the column default. That empty string then
// travels out through GET /me and blows up Intl.DateTimeFormat, while
// the web client papers over it with a locally detected zone — so the
// zone the server actually schedules with and the one the user sees
// disagree until they happen to save their profile.
func TestOIDCGithubCallback_ProvisionsUsableTimezone(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true

	newEmail := "oidc-tz-" + types.New().String() + "@example.test"
	const ghSub = "gh-tz-subject-5005"
	deps.OIDCGithub = &fakeGithubExchanger{
		claims: &internauth.GithubClaims{
			Sub:   ghSub,
			Login: "dave",
			Name:  "Dave",
			Email: newEmail,
		},
	}

	state, stateCookie := signedGithubState(t, &deps)
	_, err := OIDCGithubCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:        "auth-code",
		State:       state,
		StateCookie: stateCookie,
	})
	require.NoError(t, err)

	provisionedUID, ok := findIdentityUserID(t, db, "github", ghSub)
	require.True(t, ok)

	var tz string
	require.NoError(t, db.QueryRowContext(context.Background(),
		"SELECT timezone FROM users WHERE id = ?", provisionedUID).Scan(&tz))
	assert.NotEmpty(t, tz, "an OIDC-provisioned user must not be stored with an empty timezone")
	_, lerr := time.LoadLocation(tz)
	assert.NoError(t, lerr, "the seeded timezone must be a loadable IANA zone, got %q", tz)
}
