package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// maxFailedBeforeLock is the failed-login threshold that triggers a
// 15-minute account lockout.
const maxFailedBeforeLock = 5

// Login handles POST /auth/login. It verifies the password against the
// stored argon2id hash and rotates the failed-attempts counter.
func Login(deps Deps) func(context.Context, *LoginInput) (*LoginOutput, error) {
	return func(ctx context.Context, in *LoginInput) (*LoginOutput, error) {
		email := strings.ToLower(strings.TrimSpace(in.Body.Email))
		row, err := deps.Queries.FindLocalIdentityByEmail(ctx, email)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if row.LockedUntilAt.Valid && row.LockedUntilAt.Time.After(time.Now()) {
			return nil, httpErr(apierrors.AuthLoginAccountLocked)
		}
		if !row.PasswordHash.Valid {
			return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
		}
		ok, err := auth.VerifyPassword(row.PasswordHash.String, in.Body.Password)
		if err != nil || !ok {
			bumpFailed(ctx, deps, row)
			if row.FailedAttempts+1 >= maxFailedBeforeLock {
				return nil, httpErr(apierrors.AuthLoginRateLimitedAfterRetries)
			}
			return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
		}
		// Success: clear counters, stamp last_login.
		_ = deps.Queries.ResetIdentityFailedAttempts(ctx, row.ID)
		_ = deps.Queries.UpdateUserLastLoginAt(ctx, row.UserID)

		tokens, err := issueTokens(ctx, deps, row.UserID, row.UserPublicID)
		if err != nil {
			return nil, err
		}
		return &LoginOutput{Body: tokens}, nil
	}
}

func bumpFailed(ctx context.Context, deps Deps, row generated.FindLocalIdentityByEmailRow) {
	next := row.FailedAttempts + 1
	var lock sql.NullTime
	if next >= maxFailedBeforeLock {
		lock = sql.NullTime{Time: time.Now().Add(15 * time.Minute), Valid: true}
	}
	_ = deps.Queries.UpdateIdentityFailedAttempts(ctx, generated.UpdateIdentityFailedAttemptsParams{
		FailedAttempts: next,
		LockedUntilAt:  lock,
		ID:             row.ID,
	})
}
