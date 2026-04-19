package auth

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
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
		sess, err := deps.Sessions.FindByRefreshHash(ctx, hash)
		if err != nil {
			return out, nil
		}
		deps.Audit.Record(ctx, audit.Entry{
			Action:       "auth.logout",
			ActorID:      uint32(sess.UserID),
			ResourceType: "session",
		})
		_ = deps.Sessions.Revoke(ctx, sess.UserID, sess.PublicID)
		return out, nil
	}
}
