package auth

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
)

// Logout handles POST /auth/logout. It revokes the session matching the
// refresh token carried in the nf_rt httpOnly cookie and clears the
// cookie on the client. Errors are intentionally swallowed so logout is
// idempotent from the client's perspective.
func Logout(deps Deps) func(context.Context, *LogoutInput) (*LogoutOutput, error) {
	return func(ctx context.Context, in *LogoutInput) (*LogoutOutput, error) {
		out := &LogoutOutput{
			SetCookie: clearedRefreshCookie(deps.CookieSecure),
		}
		out.Body.Ok = true
		plain := in.RefreshCookie.Value
		if plain == "" {
			return out, nil
		}
		hash := auth.HashOpaque(plain)
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
