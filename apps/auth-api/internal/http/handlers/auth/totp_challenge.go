package auth

import (
	"context"
	"log/slog"
	"time"

	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// retireTotpChallenge claims a verified step-up challenge so it can never
// complete a second login.
//
// The challenge is a stateless JWT: everything the verifier checks
// travels inside it, so presenting it changes nothing and it stays
// redeemable for its full lifetime. Every other one-time credential in
// this flow — the magic link, the recovery code, the TOTP code's time
// step — is claimed atomically at the moment it succeeds, and the
// challenge that gates them was the one link still replayable. The OIDC
// hand-off makes that concrete: the challenge rides in a URL fragment
// and stays in browser history after the login it authorised finished.
//
// It is claimed on success rather than on presentation so that a
// mistyped code costs the user a retry, not the password step they
// already passed.
func (d Deps) retireTotpChallenge(ctx context.Context, c authn.VerifiedTotpChallenge) error {
	store := d.SingleUse
	if store == nil {
		// A nil store would silently drop the replay defence, so it is
		// treated as a wiring failure rather than an opt-out.
		slog.ErrorContext(ctx, "login_totp: single-use store not configured")
		return httpErr(apierrors.InternalUnexpected)
	}
	claimed, err := store.Consume(ctx, totpChallengeUseKey(c.TokenID), c.RemainingTTL(time.Now()))
	if err != nil {
		slog.ErrorContext(ctx, "login_totp: single-use claim failed", slog.String("err", err.Error()))
		return httpErr(apierrors.InternalUnexpected)
	}
	if !claimed {
		slog.WarnContext(ctx, "login_totp: challenge replayed")
		// Same refusal an expired or forged challenge gets, so a replay
		// is indistinguishable from any other unusable challenge.
		return httpErr(apierrors.AuthSessionExpired)
	}
	return nil
}

// totpChallengeUseKey namespaces the single-use record so challenge jtis
// can never collide with another token kind sharing the same store.
func totpChallengeUseKey(id string) string { return "totp-challenge:" + id }
