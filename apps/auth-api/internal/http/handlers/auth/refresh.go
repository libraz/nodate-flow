package auth

import (
	"context"
	"errors"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/sessionstore"
)

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
