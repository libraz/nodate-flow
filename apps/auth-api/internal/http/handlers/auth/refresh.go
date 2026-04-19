package auth

import (
	"context"
	"errors"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/sessionstore"
	apierrors "github.com/nodate-flow/nodate-flow/apps/auth-api/internal/errors"
)

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
