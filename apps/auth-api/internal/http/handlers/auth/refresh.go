package auth

import (
	"context"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/sessionstore"
)

// refreshReuseGrace is the window after a session is rotated in which
// presenting the just-superseded refresh token is treated as a benign
// concurrent refresh (two tabs, a duplicated request) rather than a theft
// signal. Inside the window the loser of the race is answered with an
// access token for the same session and no new cookie, since the winner's
// response carries the current one; outside it, the presentation is
// treated as refresh-token reuse and the entire session family is torn
// down. Kept short so a stolen token replayed later still trips the
// detector.
const refreshReuseGrace = 10 * time.Second

// sessionIdleTimeout is how long a session is allowed to sit unused
// before refresh is rejected and the user must sign in again. Wall-clock
// expiry is enforced separately via [refreshCookieTTL]; this is the
// inactivity bound, sized so a session left dormant on a stolen device
// or a parked browser tab cannot quietly mint new access tokens for
// months. 30 days matches the cookie TTL on a fresh login while still
// throttling silent indefinite re-use.
const sessionIdleTimeout = 30 * 24 * time.Hour

// Refresh handles POST /auth/refresh. It reads the refresh token from
// the nd_rt httpOnly cookie, rotates it, and issues a new access JWT.
// The rotated refresh token is returned via a Set-Cookie header; only
// the access token appears in the JSON body.
func Refresh(deps Deps) func(context.Context, *RefreshInput) (*RefreshOutput, error) {
	return func(ctx context.Context, in *RefreshInput) (*RefreshOutput, error) {
		plain := in.RefreshCookie.Value
		if plain == "" {
			return nil, httpErr(apierrors.AuthTokenRefreshInvalid)
		}
		hash := auth.HashOpaque(plain)
		sess, err := deps.Sessions.FindByRefreshHash(ctx, hash)
		if err != nil {
			if errors.Is(err, sessionstore.ErrNotFound) {
				return refreshSuperseded(ctx, deps, hash)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if sess.ExpiresAt.Before(time.Now()) {
			return nil, httpErr(apierrors.AuthTokenRefreshExpired)
		}
		// Idle-timeout guard: reject refreshes against a session that has
		// not been used within [sessionIdleTimeout]. last_used_at is
		// stamped by RotateRefreshHash, so a freshly-created session
		// (LastUsedAt == nil) falls back to CreatedAt.
		lastActive := sess.CreatedAt
		if sess.LastUsedAt != nil {
			lastActive = *sess.LastUsedAt
		}
		if time.Since(lastActive) > sessionIdleTimeout {
			return nil, httpErr(apierrors.AuthTokenRefreshExpired)
		}
		// A suspended account is refused before its token is rotated, so
		// the refusal does not also strand the client's cookie.
		pub, qerr := deps.Queries.FindUserPublicIdById(ctx, sess.UserID)
		if qerr != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(qerr, apierrors.AuthSessionRevoked, apierrors.InternalUnexpected))
		}
		newPlain, newHash, err := auth.GenerateRefresh()
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		newExp := time.Now().Add(refreshCookieTTL)
		if err := deps.Sessions.RotateRefreshHash(ctx, hash, newHash, newExp); err != nil {
			if errors.Is(err, sessionstore.ErrNotFound) {
				// A concurrent refresh rotated the token between the
				// lookup above and the row lock.
				return refreshSuperseded(ctx, deps, hash)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		access, exp, err := deps.JWT.Sign(pub, sess.PublicID)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &RefreshOutput{
			SetCookie: newRefreshCookie(newPlain, deps.CookieSecure),
			Body: AuthTokens{
				AccessToken: access,
				ExpiresAt:   exp.Unix(),
				UserID:      pub.String(),
			},
		}, nil
	}
}

// refreshSuperseded answers a refresh whose token matched no active
// session. A token the session rotated away within [refreshReuseGrace]
// lost a concurrent refresh: it is answered with an access token for that
// session and no Set-Cookie, so the winner's rotated cookie stays the one
// the client holds. Anything else is AUTH.TOKEN.REFRESH_INVALID, after
// [detectRefreshReuse] has had its chance to tear the family down.
func refreshSuperseded(ctx context.Context, deps Deps, hash string) (*RefreshOutput, error) {
	winner := detectRefreshReuse(ctx, deps, hash)
	if winner == nil || winner.RevokedAt != nil || !winner.ExpiresAt.After(time.Now()) {
		return nil, httpErr(apierrors.AuthTokenRefreshInvalid)
	}
	pub, err := deps.Queries.FindUserPublicIdById(ctx, winner.UserID)
	if err != nil {
		return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.AuthSessionRevoked, apierrors.InternalUnexpected))
	}
	access, exp, err := deps.JWT.Sign(pub, winner.PublicID)
	if err != nil {
		return nil, httpErr(apierrors.InternalUnexpected)
	}
	return &RefreshOutput{
		Body: AuthTokens{
			AccessToken: access,
			ExpiresAt:   exp.Unix(),
			UserID:      pub.String(),
		},
	}, nil
}

// detectRefreshReuse inspects a refresh hash that matched no active
// session. When the hash is one a session rotated away more than
// [refreshReuseGrace] ago, a superseded token is being replayed: every
// active session for that user is revoked (session-family teardown) and
// an "auth.refresh_reuse_detected" audit entry is written. It returns the
// rotating session only when the rotation is inside the grace window, and
// nil otherwise. Store failures are swallowed — the caller then rejects
// the token, and a detection hiccup must never change that outcome.
//
// The signal is the rotation record, not the revocation state, and the
// difference is the whole point. Asking whether the hash matches some
// revoked session's current token answers a different question: it
// says the session was signed out, which is what happens when a user
// signs out one device or changes their password. A client that then
// retries with its stale cookie is not an attacker, and treating it as
// one revoked every other session that user had deliberately kept and
// filed a high-severity finding about it. Meanwhile the case the check
// was written for could never match, because rotation overwrites the
// hash in place and leaves the superseded token resolving to nothing.
func detectRefreshReuse(ctx context.Context, deps Deps, hash string) *sessionstore.Session {
	if deps.Sessions == nil {
		return nil
	}
	superseded, err := deps.Sessions.FindSupersededBy(ctx, hash)
	if err != nil || superseded == nil {
		// The hash was never issued, or belongs to a session that was
		// signed out rather than rotated. Neither is evidence of theft.
		return nil
	}
	// Benign concurrent refresh: a client that fires two refreshes at
	// once presents the same token twice, and the second arrives just
	// after the first rotated it away.
	rotatedAt := superseded.CreatedAt
	if superseded.LastUsedAt != nil {
		rotatedAt = *superseded.LastUsedAt
	}
	if time.Since(rotatedAt) <= refreshReuseGrace {
		return superseded
	}

	if rerr := deps.Sessions.RevokeAllForUser(ctx, superseded.UserID); rerr != nil {
		// Best-effort: still record the detection below.
		_ = rerr
	}
	if deps.Audit != nil {
		resourceID := ""
		if pub, perr := deps.Queries.FindUserPublicIdById(ctx, superseded.UserID); perr == nil {
			resourceID = pub.String()
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.refresh_reuse_detected",
			ActorID:      superseded.UserID,
			ResourceType: "user",
			ResourceID:   resourceID,
			Metadata: map[string]any{
				"severity":           "high",
				"replayed_session":   superseded.PublicID.String(),
				"sessions_revoked":   "all",
				"replayed_token_age": time.Since(rotatedAt).String(),
			},
		})
	}
	return nil
}
