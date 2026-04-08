package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// Refresh handles POST /auth/refresh. It reads the refresh token from
// the nf_rt httpOnly cookie, rotates it, and issues a new access JWT.
// The rotated refresh token is returned via a Set-Cookie header; only
// the access token appears in the JSON body.
func Refresh(deps Deps) func(context.Context, *RefreshInput) (*RefreshOutput, error) {
	return func(ctx context.Context, in *RefreshInput) (*RefreshOutput, error) {
		plain := in.RefreshCookie.Value
		if plain == "" {
			return nil, httpErr(apierrors.AuthTokenRefreshInvalid)
		}
		hash := auth.HashOpaque(plain)
		row, err := deps.Queries.FindSessionByRefreshHash(ctx, hash)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.AuthTokenRefreshInvalid)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if row.ExpiresAt.Before(time.Now()) {
			return nil, httpErr(apierrors.AuthTokenRefreshExpired)
		}
		newPlain, newHash, err := auth.GenerateRefresh()
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		newExp := time.Now().Add(refreshCookieTTL)
		if err := deps.Queries.RotateSessionRefreshHash(ctx, generated.RotateSessionRefreshHashParams{
			RefreshHash: newHash,
			ExpiresAt:   newExp,
			ID:          row.ID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		var pub types.PublicID
		if err := deps.DB.QueryRowContext(ctx, `SELECT public_id FROM users WHERE id = ?`, row.UserID).Scan(&pub); err != nil {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		access, exp, err := deps.JWT.Sign(pub)
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
