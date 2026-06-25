package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internauth "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
)

// TestOIDCCallback_2FAAccountReturnsChallenge proves the L-3 fix: an OIDC
// callback that resolves a linked account which enrolled app-level TOTP
// must NOT issue session tokens. It returns a totp_required step-up
// challenge instead, forcing the second factor — closing the bypass
// where an OIDC sign-in could defeat the user's opted-in 2FA (same root
// as H-11 / B-2, which fixed the magic-link path).
func TestOIDCCallback_2FAAccountReturnsChallenge(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true
	ctx := context.Background()

	// A pre-existing account that has enrolled and confirmed app TOTP.
	uid, _, email := b2NewUser(t, deps.Queries)
	b2EnrollTotp(t, deps, uid)

	// The same email signs in via GitHub, linking the OIDC identity onto
	// the existing (TOTP-enrolled) account.
	const ghSub = "gh-totp-subject-5005"
	deps.OIDCGithub = &fakeGithubExchanger{
		claims: &internauth.GithubClaims{
			Sub:   ghSub,
			Login: "dave",
			Name:  "Dave",
			Email: email,
		},
	}

	out, err := OIDCGithubCallback(deps)(ctx, &OIDCCallbackInput{
		Code:  "auth-code",
		State: signedGithubState(t, deps),
	})
	require.NoError(t, err, "OIDC sign-in on a 2FA account must be challenged, not 500")
	require.NotNil(t, out)

	assert.Equal(t, "totp_required", out.Body.Step, "2FA account must be challenged")
	assert.NotEmpty(t, out.Body.ChallengeToken, "a challenge token must be returned")
	assert.Empty(t, out.Body.AccessToken, "no access token before the second factor")
	assert.Empty(t, out.SetCookie.Value, "no refresh cookie before the second factor")

	// The challenge must be a valid step-up token; the client presents it
	// to POST /auth/login/totp to finish login.
	pubStr, verr := deps.JWT.VerifyTotpChallenge(out.Body.ChallengeToken)
	require.NoError(t, verr)
	assert.NotEmpty(t, pubStr)

	// The OIDC identity must still have linked onto the existing user, so
	// completing the TOTP challenge logs into the right account.
	linkedUID, ok := findIdentityUserID(t, db, "github", ghSub)
	require.True(t, ok, "the github identity must have linked onto the existing user")
	assert.Equal(t, uid, linkedUID)
}

// TestOIDCCallback_NoMFACompletesDirectly guards the single-factor path:
// an OIDC sign-in for an account without app TOTP must still complete
// directly, issuing tokens and a refresh cookie.
func TestOIDCCallback_NoMFACompletesDirectly(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	deps.RegistrationOpen = true
	ctx := context.Background()

	// A brand-new email with no local TOTP enrollment.
	newEmail := "oidc-l3-" + types.New().String() + "@example.test"
	const ghSub = "gh-no-totp-subject-6006"
	deps.OIDCGithub = &fakeGithubExchanger{
		claims: &internauth.GithubClaims{
			Sub:   ghSub,
			Login: "erin",
			Name:  "Erin",
			Email: newEmail,
		},
	}

	out, err := OIDCGithubCallback(deps)(ctx, &OIDCCallbackInput{
		Code:  "auth-code",
		State: signedGithubState(t, deps),
	})
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.Equal(t, "complete", out.Body.Step, "non-2FA account must complete directly")
	assert.NotEmpty(t, out.Body.AccessToken, "tokens must be issued without a second factor")
	assert.NotEmpty(t, out.Body.UserID)
	assert.NotEmpty(t, out.SetCookie.Value, "refresh cookie set on completion")
	assert.Empty(t, out.Body.ChallengeToken, "no challenge for a non-2FA account")
}
