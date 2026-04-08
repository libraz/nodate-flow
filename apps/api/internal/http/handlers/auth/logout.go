package auth

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
)

// Logout handles POST /auth/logout. It revokes the session matching the
// provided refresh token. Errors are intentionally swallowed so logout is
// idempotent from the client's perspective.
func Logout(deps Deps) func(context.Context, *LogoutInput) (*LogoutOutput, error) {
	return func(ctx context.Context, in *LogoutInput) (*LogoutOutput, error) {
		out := &LogoutOutput{}
		out.Body.Ok = true
		if in.Body.RefreshToken == "" {
			return out, nil
		}
		hash := auth.HashOpaque(in.Body.RefreshToken)
		row, err := deps.Queries.FindSessionByRefreshHash(ctx, hash)
		if err != nil {
			return out, nil
		}
		_ = deps.Queries.RevokeSession(ctx, generated.RevokeSessionParams{
			UserID:   row.UserID,
			PublicID: row.PublicID,
		})
		return out, nil
	}
}
