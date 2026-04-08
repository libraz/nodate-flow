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

// Refresh handles POST /auth/refresh. It rotates the refresh token and
// issues a new access JWT.
func Refresh(deps Deps) func(context.Context, *RefreshInput) (*RefreshOutput, error) {
	return func(ctx context.Context, in *RefreshInput) (*RefreshOutput, error) {
		hash := auth.HashOpaque(in.Body.RefreshToken)
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
		newExp := time.Now().Add(30 * 24 * time.Hour)
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
		return &RefreshOutput{Body: AuthTokens{
			AccessToken:  access,
			RefreshToken: newPlain,
			ExpiresAt:    exp.Unix(),
			UserID:       pub.String(),
		}}, nil
	}
}
