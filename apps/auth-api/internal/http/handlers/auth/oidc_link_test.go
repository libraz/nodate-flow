package auth

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internauth "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/types"
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

// signedGithubState mints a valid signed OIDC state JWT for the GitHub
// provider so the callback's CSRF guard passes in tests.
func signedGithubState(t *testing.T, deps Deps) string {
	t.Helper()
	state, err := deps.JWT.SignOIDCStateForProvider("nonce-value", "github")
	require.NoError(t, err)
	return state
}

// signedMicrosoftState mints a valid signed OIDC state JWT for Microsoft.
func signedMicrosoftState(t *testing.T, deps Deps) string {
	t.Helper()
	state, err := deps.JWT.SignOIDCStateForProvider("nonce-value", "microsoft")
	require.NoError(t, err)
	return state
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

	out, err := OIDCGithubCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:  "auth-code",
		State: signedGithubState(t, deps),
	})
	require.NoError(t, err, "same-email GitHub sign-in must link, not 500")
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Body.AccessToken, "linked sign-in must issue an access token")
	assert.NotEmpty(t, out.SetCookie.Value, "linked sign-in must set a refresh cookie")

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

	out, err := OIDCMicrosoftCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:  "auth-code",
		State: signedMicrosoftState(t, deps),
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

	out, err := OIDCGithubCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:  "auth-code",
		State: signedGithubState(t, deps),
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

	out, err := OIDCGithubCallback(deps)(context.Background(), &OIDCCallbackInput{
		Code:  "auth-code",
		State: signedGithubState(t, deps),
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
