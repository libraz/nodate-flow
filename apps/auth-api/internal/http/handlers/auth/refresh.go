package auth

import (
	"context"
	"errors"
	"time"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth"
	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/sessionstore"
)

// refreshReuseGrace is the window after a session is rotated/revoked in
// which presenting the just-superseded refresh token is treated as a
// benign double-submit (browser retry, duplicated request) rather than a
// theft signal. Inside the window the stale token is simply rejected;
// outside it, the presentation is treated as refresh-token reuse and the
// entire session family is torn down. Kept short so a real stolen token
// replayed minutes later still trips the detector.
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
				// The hash matched no active session. Before rejecting,
				// check whether some session rotated this token away:
				// that means a superseded refresh token was replayed
				// (token theft or a leaked cookie), so the whole session
				// family is torn down and the event is audited. A
				// rotation that just happened is treated as a benign
				// double-submit and only rejected.
				detectRefreshReuse(ctx, deps, hash)
				return nil, httpErr(apierrors.AuthTokenRefreshInvalid)
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
		newPlain, newHash, err := auth.GenerateRefresh()
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		newExp := time.Now().Add(refreshCookieTTL)
		if err := deps.Sessions.RotateRefreshHash(ctx, hash, newHash, newExp); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		pub, qerr := deps.Queries.FindUserPublicIdById(ctx, sess.UserID)
		if qerr != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
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

// detectRefreshReuse inspects a refresh hash that matched no active
// session. When the hash is one a session rotated away, a superseded
// token is being replayed: every active session for that user is
// revoked (session-family teardown) and an
// "auth.refresh_reuse_detected" audit entry is written. Failures are
// swallowed — this runs on a path that already returns
// AUTH.TOKEN.REFRESH_INVALID, and a detection hiccup must never change
// the user-visible outcome.
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
func detectRefreshReuse(ctx context.Context, deps Deps, hash string) {
	if deps.Sessions == nil {
		return
	}
	superseded, err := deps.Sessions.FindSupersededBy(ctx, hash)
	if err != nil || superseded == nil {
		// The hash was never issued, or belongs to a session that was
		// signed out rather than rotated. Either way the caller has
		// already rejected it, and neither is evidence of theft.
		return
	}
	// Benign double-submit grace: a client that fires two refreshes at
	// once presents the same token twice, and the second arrives just
	// after the first rotated it away. Reject it — the caller does —
	// but do not tear the family down over a duplicated request.
	rotatedAt := superseded.CreatedAt
	if superseded.LastUsedAt != nil {
		rotatedAt = *superseded.LastUsedAt
	}
	if time.Since(rotatedAt) <= refreshReuseGrace {
		return
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
}
