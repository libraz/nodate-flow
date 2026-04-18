package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
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
				// Run Argon2id against a dummy hash to equalise
				// timing so attackers cannot enumerate valid emails
				// by measuring response latency.
				_, _ = auth.VerifyPassword(auth.DummyHash(), in.Body.Password)
				return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if row.LockedUntilAt.Valid && row.LockedUntilAt.Time.After(time.Now()) {
			// Also equalise timing for locked accounts.
			_, _ = auth.VerifyPassword(auth.DummyHash(), in.Body.Password)
			return nil, httpErr(apierrors.AuthLoginAccountLocked)
		}
		if !row.PasswordHash.Valid {
			_, _ = auth.VerifyPassword(auth.DummyHash(), in.Body.Password)
			return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
		}
		ok, err := auth.VerifyPassword(row.PasswordHash.String, in.Body.Password)
		if err != nil || !ok {
			bumpFailed(ctx, deps, row)
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "auth.login_failed",
				ActorID:      uint32(row.UserID),
				ResourceType: "user",
				Metadata:     map[string]any{"email": email},
			})
			if row.FailedAttempts+1 >= maxFailedBeforeLock {
				return nil, httpErr(apierrors.AuthLoginRateLimitedAfterRetries)
			}
			return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
		}
		// Password OK. Clear counters immediately so a second-factor
		// failure does not re-trigger the lockout counter.
		if err := deps.Queries.ResetIdentityFailedAttempts(ctx, row.ID); err != nil {
			slog.ErrorContext(ctx, "login: failed to reset failed attempts", slog.Any("err", err), slog.Int("identity_id", int(row.ID)))
		}

		// If the account has confirmed TOTP, do NOT issue session
		// tokens yet. Return a short-lived challenge instead; the
		// client must finish at POST /auth/login/totp. last_login_at
		// is deferred to that happy path.
		if row.MfaConfirmedAt.Valid && len(row.MfaSecretCiphertext.String) > 0 {
			challenge, _, cerr := deps.JWT.SignTotpChallenge(row.UserID)
			if cerr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			return &LoginOutput{
				Body: LoginBody{
					Step:           "totp_required",
					ChallengeToken: challenge,
				},
			}, nil
		}

		if err := deps.Queries.UpdateUserLastLoginAt(ctx, row.UserID); err != nil {
			slog.ErrorContext(ctx, "login: failed to update last_login_at", slog.Any("err", err), slog.Int("user_id", int(row.UserID)))
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.login",
			ActorID:      uint32(row.UserID),
			ResourceType: "user",
			Metadata:     map[string]any{"email": email},
		})
		tokens, refresh, err := issueTokens(ctx, deps, row.UserID, row.UserPublicID, in.UserAgent, middleware.ClientIPFromContext(ctx))
		if err != nil {
			return nil, err
		}
		return &LoginOutput{
			SetCookie: newRefreshCookie(refresh, deps.CookieSecure),
			Body: LoginBody{
				Step:        "complete",
				AccessToken: tokens.AccessToken,
				ExpiresAt:   tokens.ExpiresAt,
				UserID:      tokens.UserID,
			},
		}, nil
	}
}

// LoginTotp handles POST /auth/login/totp. It validates the
// step-up challenge from a previous /auth/login call, verifies the
// submitted 6-digit code against the user's stored TOTP secret,
// then issues real session tokens. This is the only path on which
// last_login_at is stamped for a 2FA-enabled account.
func LoginTotp(deps Deps) func(context.Context, *LoginTotpInput) (*LoginTotpOutput, error) {
	return func(ctx context.Context, in *LoginTotpInput) (*LoginTotpOutput, error) {
		if deps.Cipher == nil {
			return nil, httpErr(apierrors.AuthTotpNotConfigured)
		}
		uid, err := deps.JWT.VerifyTotpChallenge(in.Body.ChallengeToken)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionExpired)
		}
		ident, err := deps.Queries.FindLocalIdentityByUserId(ctx, uid)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if !ident.MfaConfirmedAt.Valid || len(ident.MfaSecretCiphertext.String) == 0 {
			// TOTP was disabled between /auth/login and here. Refuse
			// the challenge so the client retries single-factor
			// login instead of silently completing on stale state.
			return nil, httpErr(apierrors.AuthTotpNotEnrolled)
		}
		hasCode := strings.TrimSpace(in.Body.Code) != ""
		hasRecovery := strings.TrimSpace(in.Body.RecoveryCode) != ""
		if hasCode == hasRecovery {
			// Either both supplied or neither.
			return nil, httpErr(apierrors.AuthTotpRecoveryCodeRequired)
		}
		if hasCode {
			secret, derr := deps.Cipher.Decrypt([]byte(ident.MfaSecretCiphertext.String))
			if derr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			if !auth.VerifyTotp(secret, in.Body.Code, time.Now()) {
				return nil, httpErr(apierrors.AuthTotpCodeMismatch)
			}
		} else {
			hash := auth.HashRecoveryCode(in.Body.RecoveryCode)
			rcID, lerr := deps.Queries.FindUnusedRecoveryCode(ctx, generated.FindUnusedRecoveryCodeParams{UserID: uid, CodeHash: hash})
			if lerr != nil {
				if errors.Is(lerr, sql.ErrNoRows) {
					return nil, httpErr(apierrors.AuthTotpRecoveryCodeInvalid)
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			if merr := deps.Queries.MarkRecoveryCodeUsed(ctx, rcID); merr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}
		u, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err := deps.Queries.UpdateUserLastLoginAt(ctx, uid); err != nil {
			slog.ErrorContext(ctx, "login_totp: failed to update last_login_at", slog.Any("err", err), slog.Int("user_id", int(uid)))
		}
		tokens, refresh, err := issueTokens(ctx, deps, uid, u.PublicID, in.UserAgent, middleware.ClientIPFromContext(ctx))
		if err != nil {
			return nil, err
		}
		return &LoginTotpOutput{
			SetCookie: newRefreshCookie(refresh, deps.CookieSecure),
			Body:      tokens,
		}, nil
	}
}

func bumpFailed(ctx context.Context, deps Deps, row generated.FindLocalIdentityByEmailRow) {
	next := row.FailedAttempts + 1
	var lock sql.NullTime
	if next >= maxFailedBeforeLock {
		lock = sql.NullTime{Time: time.Now().Add(15 * time.Minute), Valid: true}
	}
	if err := deps.Queries.UpdateIdentityFailedAttempts(ctx, generated.UpdateIdentityFailedAttemptsParams{
		FailedAttempts: next,
		LockedUntilAt:  lock,
		ID:             row.ID,
	}); err != nil {
		slog.ErrorContext(ctx, "login: failed to bump failed attempts counter", slog.Any("err", err), slog.Int("identity_id", int(row.ID)))
	}
}
