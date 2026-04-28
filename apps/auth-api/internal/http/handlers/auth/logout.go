package auth

import (
	"context"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
)

// Logout handles POST /auth/logout. It revokes the session matching the
// refresh token carried in the nd_rt httpOnly cookie and clears the
// cookie on the client. The handler is idempotent from the client's
// perspective: every code path returns 200 with the cleared cookie so
// re-clicking "log out" never surfaces an error. Internally we still
// log Revoke failures and emit an audit entry on every successful
// revoke so investigators can correlate the session lifecycle.
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
			// No active session matched — either the cookie was
			// already invalid or the user double-clicked. Either
			// way, return ok so the client clears its cookie and
			// stops retrying.
			return out, nil
		}
		if revokeErr := deps.Sessions.Revoke(ctx, sess.UserID, sess.PublicID); revokeErr != nil {
			// We do not fail the request: returning 5xx here would
			// leave the client confused about whether they are
			// signed out. Log loudly so a broken session store is
			// still visible in metrics / alerts.
			slog.WarnContext(ctx, "logout: session revoke failed",
				slog.String("err", revokeErr.Error()),
				slog.String("session_id", sess.PublicID.String()))
			return out, nil
		}
		recordLogoutAudit(ctx, deps, sess.UserID, sess.PublicID.String())
		return out, nil
	}
}

// recordLogoutAudit emits the audit entry for a successful logout. The
// metadata carries the public id of the session that was revoked so
// investigators can match the logout against the session lifecycle in
// the audit feed. Nil-safe so handlers do not need to guard the call.
func recordLogoutAudit(ctx context.Context, deps Deps, uid uint32, sessionPublicID string) {
	if deps.Audit == nil {
		return
	}
	deps.Audit.Record(ctx, audit.Entry{
		Action:       "auth.logout",
		ActorID:      uid,
		ResourceType: "session",
		ResourceID:   sessionPublicID,
		Metadata: map[string]any{
			"session_id": sessionPublicID,
		},
	})
}
