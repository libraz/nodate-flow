package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internauth "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
)

// loginTotpCodeBody mirrors the anonymous LoginTotpInput body struct for
// the authenticator-code branch, so call sites do not repeat the tags.
func loginTotpCodeBody(challenge, code string) *LoginTotpInput {
	return &LoginTotpInput{
		Body: struct {
			ChallengeToken string `json:"challengeToken" minLength:"1"`
			Code           string `json:"code,omitempty" pattern:"^$|^[0-9]{6}$"`
			RecoveryCode   string `json:"recoveryCode,omitempty" pattern:"^$|^[A-Za-z0-9-]{10,20}$"`
		}{ChallengeToken: challenge, Code: code},
	}
}

// TestLoginTotp_ChallengeIsSingleUse proves a step-up challenge cannot
// complete a second login inside its five-minute lifetime.
//
// The second attempt deliberately carries the NEXT time-step's code, so
// the one-time-use guard on the TOTP code itself cannot be what rejects
// it: without a claim on the challenge, that submission is a perfectly
// valid second login. The challenge is where the OIDC hand-off leaves a
// copy behind — it travels in a URL fragment and survives in browser
// history after the login it authorised finished.
func TestLoginTotp_ChallengeIsSingleUse(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	ctx := context.Background()

	uid, pub, _ := b2NewUser(t, deps.Queries)
	secret := b2EnrollTotp(t, deps, uid)

	challenge, _, err := deps.JWT.SignTotpChallenge(pub.String())
	require.NoError(t, err)

	now := time.Now()
	out, err := LoginTotp(deps)(ctx, loginTotpCodeBody(challenge, internauth.TotpCode(secret, now)))
	require.NoError(t, err, "the first redemption of a fresh challenge must succeed")
	require.NotNil(t, out)
	require.NotEmpty(t, out.Body.AccessToken)

	// A code from the next time-step is still inside the +/-1 window and
	// is a step the account has not consumed, so nothing but the
	// challenge claim can refuse this request.
	next := internauth.TotpCode(secret, now.Add(30*time.Second))
	second, err := LoginTotp(deps)(ctx, loginTotpCodeBody(challenge, next))
	assert.Nil(t, second, "a replayed challenge must not issue a second session")
	problem := problemFor(t, err)
	assert.Equal(t, apierrors.AuthSessionExpired.Code, problem.Type,
		"a spent challenge must be refused the same way an expired one is")
}

// TestLoginTotp_ConcurrentSameChallenge_SingleUse proves the claim holds
// under contention: several logins racing one challenge and one code must
// elect exactly one winner. Sequential retirement alone would leave a
// window where every racer verifies the second factor before any of them
// records the redemption.
func TestLoginTotp_ConcurrentSameChallenge_SingleUse(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	ctx := context.Background()

	// Rounds keep a lucky interleaving from passing the whole test: a
	// broken claim has to lose every round to look correct.
	const (
		rounds = 8
		racers = 4
	)
	handler := LoginTotp(deps)
	for round := 0; round < rounds; round++ {
		uid, pub, _ := b2NewUser(t, deps.Queries)
		secret := b2EnrollTotp(t, deps, uid)
		challenge, _, err := deps.JWT.SignTotpChallenge(pub.String())
		require.NoError(t, err)
		code := internauth.TotpCode(secret, time.Now())

		var (
			wg         sync.WaitGroup
			mu         sync.Mutex
			okCount    int
			spentCount int
			unexpected error
			start      = make(chan struct{})
		)
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				out, herr := handler(ctx, loginTotpCodeBody(challenge, code))
				mu.Lock()
				defer mu.Unlock()
				if herr == nil {
					if out != nil && out.Body.AccessToken != "" {
						okCount++
					}
					return
				}
				var problem *handlerutil.ProblemDetails
				if errors.As(herr, &problem) && problem.Type == apierrors.AuthSessionExpired.Code {
					spentCount++
					return
				}
				unexpected = herr
			}()
		}
		close(start)
		wg.Wait()

		require.NoError(t, unexpected,
			"round %d: a racer that lost the challenge claim must be told the challenge is spent", round)
		require.Equal(t, 1, okCount, "round %d: exactly one racer may redeem the challenge", round)
		require.Equal(t, racers-1, spentCount, "round %d: every other racer must be refused", round)
	}
}

// TestLoginTotp_FailedCodeKeepsChallengeUsable pins the reason the claim
// happens on success rather than on presentation: a mistyped code must
// cost the user a retry, not the password step they already passed.
func TestLoginTotp_FailedCodeKeepsChallengeUsable(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	ctx := context.Background()

	uid, pub, _ := b2NewUser(t, deps.Queries)
	secret := b2EnrollTotp(t, deps, uid)

	challenge, _, err := deps.JWT.SignTotpChallenge(pub.String())
	require.NoError(t, err)

	_, err = LoginTotp(deps)(ctx, loginTotpCodeBody(challenge, "000000"))
	problem := problemFor(t, err)
	require.Equal(t, apierrors.AuthTotpCodeMismatch.Code, problem.Type)

	out, err := LoginTotp(deps)(ctx, loginTotpCodeBody(challenge, internauth.TotpCode(secret, time.Now())))
	require.NoError(t, err, "a wrong code must not burn the challenge")
	require.NotNil(t, out)
	assert.NotEmpty(t, out.Body.AccessToken)
}

// TestLoginTotp_FailsClosedWithoutSingleUseStore asserts a missing store
// is a wiring failure, not a silent opt-out of the replay defence. A
// deployment that drops the store must stop completing step-up logins
// rather than quietly hand back a reusable challenge.
func TestLoginTotp_FailsClosedWithoutSingleUseStore(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	ctx := context.Background()

	uid, pub, _ := b2NewUser(t, deps.Queries)
	secret := b2EnrollTotp(t, deps, uid)
	deps.SingleUse = nil

	challenge, _, err := deps.JWT.SignTotpChallenge(pub.String())
	require.NoError(t, err)

	out, err := LoginTotp(deps)(ctx, loginTotpCodeBody(challenge, internauth.TotpCode(secret, time.Now())))
	assert.Nil(t, out)
	problem := problemFor(t, err)
	assert.Equal(t, apierrors.InternalUnexpected.Code, problem.Type)
}
