package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// maxFailedBeforeLock is the failed-login threshold that triggers a
// 15-minute account lockout for password and TOTP code paths.
const maxFailedBeforeLock = 5

// maxRecoveryFailedBeforeLock is the tighter threshold applied when the
// failure happened on the recovery-code branch of the TOTP step. The
// recovery counter shares storage with the TOTP failed-attempts column;
// the lower threshold makes recovery-code brute-forcing trip the lockout
// faster without giving up the timing-uniform single-counter design.
const maxRecoveryFailedBeforeLock = 3

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
			// The account is locked, but we must not tell an
			// unauthenticated caller that: a distinct "account locked"
			// response only ever appears for a real, existing email, so
			// it becomes an enumeration oracle (an unknown email can
			// never accumulate a lock and always sees invalid
			// credentials). Collapse it into the same invalid-credentials
			// code and equalise timing with a dummy verify. The lockout
			// itself is still fully enforced — the early return below
			// means even the correct password cannot authenticate while
			// the lock window is active.
			_, _ = auth.VerifyPassword(auth.DummyHash(), in.Body.Password)
			return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
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
			// bumpFailed above still sets locked_until_at once the
			// threshold is reached, so the lockout remains fully in force.
			// We deliberately do NOT surface a distinct rate-limited /
			// locked response here: only a real email can ever reach the
			// threshold, so a different code at attempt N would let an
			// attacker enumerate valid accounts by watching for the
			// response to change. Every failed attempt — before and after
			// the threshold — returns the same invalid-credentials code.
			return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
		}
		// Password OK. Clear counters immediately so a second-factor
		// failure does not re-trigger the lockout counter.
		if err := deps.Queries.ResetIdentityFailedAttempts(ctx, row.ID); err != nil {
			slog.ErrorContext(ctx, "login: failed to reset failed attempts", slog.Any("err", err), slog.String("user_public_id", row.UserPublicID.String()))
		}

		// If the account has confirmed TOTP, do NOT issue session
		// tokens yet. Return a short-lived challenge instead; the
		// client must finish at POST /auth/login/totp. last_login_at
		// is deferred to that happy path.
		if row.MfaConfirmedAt.Valid && len(row.MfaSecretCiphertext.String) > 0 {
			challenge, _, cerr := deps.JWT.SignTotpChallenge(row.UserPublicID.String())
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
			slog.ErrorContext(ctx, "login: failed to update last_login_at", slog.Any("err", err), slog.String("user_public_id", row.UserPublicID.String()))
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.login",
			ActorID:      uint32(row.UserID),
			ResourceType: "user",
			Metadata:     map[string]any{"email": email},
		})
		tokens, refresh, err := IssueTokens(ctx, deps, row.UserID, row.UserPublicID, in.UserAgent, authn.ClientIPFromContext(ctx))
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
		pubStr, err := deps.JWT.VerifyTotpChallenge(in.Body.ChallengeToken)
		if err != nil {
			return nil, httpErr(apierrors.AuthSessionExpired)
		}
		pubID, perr := dbtype.Parse(pubStr)
		if perr != nil {
			return nil, httpErr(apierrors.AuthSessionExpired)
		}
		uid, err := deps.Queries.FindUserInternalIdByPublicId(ctx, pubID)
		if err != nil {
			return nil, httpErr(apierrors.AuthLoginInvalidCredentials)
		}
		ident, err := deps.Queries.FindLocalIdentityByUserId(ctx, uid)
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.AuthLoginInvalidCredentials, apierrors.InternalUnexpected))
		}
		if ident.LockedUntilAt.Valid && ident.LockedUntilAt.Time.After(time.Now()) {
			return nil, httpErr(apierrors.AuthLoginAccountLocked)
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
		// acceptedStep carries the matched TOTP time-step from the
		// authenticator branch so it can be persisted as the new
		// last-used step after the success path resets failed attempts.
		// It stays -1 on the recovery-code branch (no step to advance).
		acceptedStep := int64(-1)
		if hasCode {
			secret, derr := deps.Cipher.Decrypt([]byte(ident.MfaSecretCiphertext.String))
			if derr != nil {
				recordCipherDecryptFailure(ctx, deps, uint32(uid), "login_totp", derr)
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			step, okCode := auth.VerifyTotpStep(secret, in.Body.Code, time.Now())
			// RFC 6238 5.2 one-time-use: a syntactically valid code whose
			// step was already consumed is a replay. Treat it exactly like
			// a mismatch (same audit + lockout accounting) so an attacker
			// replaying a captured code cannot distinguish replay from a
			// wrong code and cannot reuse it inside the skew window.
			replayed := okCode && ident.MfaLastStep.Valid && step <= ident.MfaLastStep.Int64
			if !okCode || replayed {
				bumpFailedByID(ctx, deps, ident.ID, ident.FailedAttempts)
				deps.Audit.Record(ctx, audit.Entry{
					Action:       "auth.login_totp_failed",
					ActorID:      uint32(uid),
					ResourceType: "user",
				})
				if ident.FailedAttempts+1 >= maxFailedBeforeLock {
					return nil, httpErr(apierrors.AuthLoginRateLimitedAfterRetries)
				}
				return nil, httpErr(apierrors.AuthTotpCodeMismatch)
			}
			acceptedStep = step
		} else {
			hash := auth.HashRecoveryCode(in.Body.RecoveryCode)
			rcID, lerr := deps.Queries.FindUnusedRecoveryCode(ctx, generated.FindUnusedRecoveryCodeParams{UserID: uid, CodeHash: hash})
			recordRecoveryFailure := func() error {
				bumpFailedByIDWithThreshold(ctx, deps, ident.ID, ident.FailedAttempts, maxRecoveryFailedBeforeLock)
				deps.Audit.Record(ctx, audit.Entry{
					Action:       "auth.login_totp_failed",
					ActorID:      uint32(uid),
					ResourceType: "user",
					Metadata:     map[string]any{"method": "recovery_code"},
				})
				if ident.FailedAttempts+1 >= maxRecoveryFailedBeforeLock {
					return httpErr(apierrors.AuthLoginRateLimitedAfterRetries)
				}
				return httpErr(apierrors.AuthTotpRecoveryCodeInvalid)
			}
			if lerr != nil {
				if errors.Is(lerr, sql.ErrNoRows) {
					return nil, recordRecoveryFailure()
				}
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			// Defense-in-depth: even though the SQL WHERE clause
			// filtered by code_hash, re-verify the supplied code
			// re-hashes to the same digest using a constant-time
			// comparator. A mismatch here cannot happen on a healthy
			// happy path (the row was selected by hash equality) but
			// the explicit check guards against future refactors that
			// might reuse the lookup against an in-memory cache where
			// no storage layer enforces equality, and keeps the
			// comparison time independent of the divergence offset.
			if !auth.VerifyRecoveryCodeHash(in.Body.RecoveryCode, hash) {
				return nil, recordRecoveryFailure()
			}
			// Atomic single-use claim: the UPDATE only matches while
			// used_at IS NULL, so two logins racing on the same
			// recovery code elect exactly one winner. Zero affected
			// rows means a concurrent request consumed the code
			// between the lookup above and this claim; treat it
			// exactly like an invalid code (same audit + lockout
			// accounting) so a race loss is indistinguishable from a
			// mismatch and the code can never be redeemed twice.
			claimed, merr := deps.Queries.MarkRecoveryCodeUsed(ctx, rcID)
			if merr != nil {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
			if claimed == 0 {
				return nil, recordRecoveryFailure()
			}
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "auth.recovery_code_used",
				ActorID:      uint32(uid),
				ResourceType: "user",
			})
		}
		// 2FA succeeded — clear any accumulated failed attempts.
		if err := deps.Queries.ResetIdentityFailedAttempts(ctx, ident.ID); err != nil {
			slog.ErrorContext(ctx, "login_totp: failed to reset failed attempts", slog.Any("err", err))
		}
		// Advance the one-time-use TOTP step so the accepted code (and
		// every earlier code in the window) cannot be replayed. Skipped
		// on the recovery-code branch, which has no time-step.
		if acceptedStep >= 0 {
			if err := deps.Queries.UpdateIdentityMfaLastStep(ctx, generated.UpdateIdentityMfaLastStepParams{
				Step: sql.NullInt64{Int64: acceptedStep, Valid: true},
				ID:   ident.ID,
			}); err != nil {
				slog.ErrorContext(ctx, "login_totp: failed to advance mfa last step", slog.Any("err", err))
			}
		}
		u, err := deps.Queries.FindUserProfileById(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err := deps.Queries.UpdateUserLastLoginAt(ctx, uid); err != nil {
			slog.ErrorContext(ctx, "login_totp: failed to update last_login_at", slog.Any("err", err), slog.String("user_public_id", u.PublicID.String()))
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.login_totp",
			ActorID:      uint32(uid),
			ResourceType: "user",
		})
		tokens, refresh, err := IssueTokens(ctx, deps, uid, u.PublicID, in.UserAgent, authn.ClientIPFromContext(ctx))
		if err != nil {
			return nil, err
		}
		return &LoginTotpOutput{
			SetCookie: newRefreshCookie(refresh, deps.CookieSecure),
			Body:      tokens,
		}, nil
	}
}

// bumpFailedByID increments the failed-attempts counter on an identity
// identified by its internal ID, locking the account for 15 minutes if
// the count reaches [maxFailedBeforeLock]. Used by the TOTP login path
// where we already have the identity row from FindLocalIdentityByUserId.
func bumpFailedByID(ctx context.Context, deps Deps, identityID uint32, currentAttempts uint32) {
	bumpFailedByIDWithThreshold(ctx, deps, identityID, currentAttempts, maxFailedBeforeLock)
}

// bumpFailedByIDWithThreshold is the threshold-parameterised variant of
// [bumpFailedByID]. The recovery-code branch passes the lower
// [maxRecoveryFailedBeforeLock] so an attacker probing recovery codes
// trips the 15-minute lockout faster than an attacker brute-forcing
// 6-digit TOTP codes.
func bumpFailedByIDWithThreshold(ctx context.Context, deps Deps, identityID uint32, currentAttempts, threshold uint32) {
	next := currentAttempts + 1
	var lock sql.NullTime
	if next >= threshold {
		lock = sql.NullTime{Time: time.Now().Add(15 * time.Minute), Valid: true}
	}
	if err := deps.Queries.UpdateIdentityFailedAttempts(ctx, generated.UpdateIdentityFailedAttemptsParams{
		FailedAttempts: next,
		LockedUntilAt:  lock,
		ID:             identityID,
	}); err != nil {
		slog.ErrorContext(ctx, "login_totp: failed to bump failed attempts counter", slog.Any("err", err))
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
		slog.ErrorContext(ctx, "login: failed to bump failed attempts counter", slog.Any("err", err), slog.String("user_public_id", row.UserPublicID.String()))
	}
}

// recordCipherDecryptFailure logs a high-severity audit entry whenever
// the application cipher fails to decrypt a stored ciphertext. This is
// almost always evidence of key rotation drift or storage tampering;
// surfacing it through audit (and not just logs) lets operators trace
// the affected user and timestamp without having to grep server logs.
func recordCipherDecryptFailure(ctx context.Context, deps Deps, actorID uint32, contextLabel string, derr error) {
	slog.ErrorContext(ctx, "auth: cipher decrypt failed", slog.String("context", contextLabel), slog.Any("err", derr))
	if deps.Audit == nil {
		return
	}
	deps.Audit.Record(ctx, audit.Entry{
		Action:       "auth.cipher_decrypt_failed",
		ActorID:      actorID,
		ResourceType: "user",
		Metadata: map[string]any{
			"context":  contextLabel,
			"severity": "high",
		},
	})
}
