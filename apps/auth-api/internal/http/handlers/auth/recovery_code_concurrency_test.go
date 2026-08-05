package auth

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internauth "github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
)

// loginTotpBody mirrors the anonymous LoginTotpInput body struct so the
// concurrency test can build requests without repeating the struct tags
// at every call site.
func loginTotpBody(challenge, recoveryCode string) *LoginTotpInput {
	return &LoginTotpInput{
		Body: struct {
			ChallengeToken string `json:"challengeToken" minLength:"1"`
			Code           string `json:"code,omitempty" pattern:"^$|^[0-9]{6}$"`
			RecoveryCode   string `json:"recoveryCode,omitempty" pattern:"^$|^[A-Za-z0-9-]{10,20}$"`
		}{ChallengeToken: challenge, RecoveryCode: recoveryCode},
	}
}

// TestLoginTotp_ConcurrentRecoveryCode_SingleUse proves the single-use
// guarantee of recovery codes under concurrency. Two logins race the
// same recovery code through POST /auth/login/totp; the atomic claim
// (MarkRecoveryCodeUsed guarded by used_at IS NULL) must admit exactly
// one of them. The loser must receive AUTH.TOTP.RECOVERY_CODE_INVALID,
// never a second session. Without the RowsAffected check the
// lookup-then-mark sequence let both requests observe the code as
// unused and both complete login — the classic TOCTOU double-spend
// this test pins.
func TestLoginTotp_ConcurrentRecoveryCode_SingleUse(t *testing.T) {
	t.Parallel()
	db := requireB2DB(t)
	deps, _ := b2Deps(t, db)
	ctx := context.Background()

	uid, pub, _ := b2NewUser(t, deps.Queries)
	b2EnrollTotp(t, deps, uid)

	// Mint a single recovery code for the account.
	const recoveryCode = "RACE-CODE-12345"
	require.NoError(t, deps.Queries.InsertRecoveryCode(ctx, generated.InsertRecoveryCodeParams{
		UserID:   uid,
		CodeHash: internauth.HashRecoveryCode(recoveryCode),
	}))

	// Each racer carries its own valid step-up challenge; only the
	// recovery code is shared.
	const racers = 2
	challenges := make([]string, racers)
	for i := range challenges {
		c, _, err := deps.JWT.SignTotpChallenge(pub.String())
		require.NoError(t, err)
		challenges[i] = c
	}

	handler := LoginTotp(deps)
	var (
		wg            sync.WaitGroup
		mu            sync.Mutex
		okCount       int
		invalidCount  int
		unexpectedErr error
		start         = make(chan struct{})
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(challenge string) {
			defer wg.Done()
			<-start // release all goroutines simultaneously
			out, herr := handler(ctx, loginTotpBody(challenge, recoveryCode))
			mu.Lock()
			defer mu.Unlock()
			if herr == nil {
				if out != nil && out.Body.AccessToken != "" {
					okCount++
				}
				return
			}
			var problem *handlerutil.ProblemDetails
			if errors.As(herr, &problem) && problem.Type == apierrors.AuthTotpRecoveryCodeInvalid.Code {
				invalidCount++
			} else {
				unexpectedErr = herr
			}
		}(challenges[i])
	}
	close(start)
	wg.Wait()

	require.NoError(t, unexpectedErr,
		"the race loser must fail with RECOVERY_CODE_INVALID, not an unrelated error")
	assert.Equal(t, 1, okCount, "exactly one concurrent login may consume the recovery code")
	assert.Equal(t, racers-1, invalidCount,
		"every other racer must be told the recovery code is invalid")

	// Authoritative DB check: the code must be marked used exactly once
	// and no unused copy may remain.
	unused, err := deps.Queries.CountActiveRecoveryCodes(ctx, uid)
	require.NoError(t, err)
	assert.Zero(t, unused, "no unused recovery code may remain after the race")

	var usedCount int64
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = ? AND used_at IS NOT NULL",
		uid,
	).Scan(&usedCount))
	assert.Equal(t, int64(1), usedCount, "the recovery code row must be consumed exactly once")
}
