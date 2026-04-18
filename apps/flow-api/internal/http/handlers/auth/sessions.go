// Sessions management handlers for /settings/security. All three
// operations are user-scoped and read the authenticated user id from
// the auth middleware. The current session is identified by hashing
// the plaintext refresh token carried in the nf_rt cookie.
package auth

import (
	"context"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// ListSessions handles GET /me/sessions. It returns every active
// session for the caller, marking the one matching the current
// refresh cookie so the UI can disable its Revoke button.
func ListSessions(deps Deps) func(context.Context, *ListSessionsInput) (*ListSessionsOutput, error) {
	return func(ctx context.Context, in *ListSessionsInput) (*ListSessionsOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		sessions, err := deps.Sessions.ListActive(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		currentPub, hasCurrent := currentSessionPublicID(ctx, deps, in.RefreshCookie.Value)

		out := &ListSessionsOutput{}
		out.Body.Items = make([]SessionSummary, 0, len(sessions))
		for _, s := range sessions {
			item := SessionSummary{
				ID:        s.PublicID.String(),
				UserAgent: s.UserAgent,
				IPAddress: s.IPAddress,
				Current:   hasCurrent && s.PublicID == currentPub,
				CreatedAt: s.CreatedAt.Unix(),
				ExpiresAt: s.ExpiresAt.Unix(),
			}
			if s.LastUsedAt != nil {
				ts := s.LastUsedAt.Unix()
				item.LastUsedAt = &ts
			}
			out.Body.Items = append(out.Body.Items, item)
		}
		return out, nil
	}
}

// RevokeOneSession handles DELETE /me/sessions/{sessionId}. Idempotent:
// a missing or already-revoked session still returns ok=true.
func RevokeOneSession(deps Deps) func(context.Context, *RevokeSessionInput) (*RevokeSessionOutput, error) {
	return func(ctx context.Context, in *RevokeSessionInput) (*RevokeSessionOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		pub, err := types.Parse(in.SessionID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		if err := deps.Sessions.Revoke(ctx, uid, pub); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		out := &RevokeSessionOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// RevokeAllOtherSessions handles DELETE /me/sessions. It revokes every
// active session for the caller except the one matching the nf_rt
// cookie on the current request. If the cookie is missing the handler
// refuses to revoke (otherwise it would log the caller out as a side
// effect); that is signalled as AUTH.SESSION.REVOKED so the UI asks
// the user to sign in again.
func RevokeAllOtherSessions(deps Deps) func(context.Context, *RevokeAllOtherSessionsInput) (*RevokeAllOtherSessionsOutput, error) {
	return func(ctx context.Context, in *RevokeAllOtherSessionsInput) (*RevokeAllOtherSessionsOutput, error) {
		uid, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		currentPub, hasCurrent := currentSessionPublicID(ctx, deps, in.RefreshCookie.Value)
		if !hasCurrent {
			return nil, httpErr(apierrors.AuthSessionRevoked)
		}
		before, err := deps.Sessions.ListActive(ctx, uid)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err := deps.Sessions.RevokeAllExcept(ctx, uid, currentPub); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		revoked := 0
		for _, s := range before {
			if s.PublicID != currentPub {
				revoked++
			}
		}
		out := &RevokeAllOtherSessionsOutput{}
		out.Body.Ok = true
		out.Body.Revoked = revoked
		return out, nil
	}
}

// currentSessionPublicID resolves the caller's current session public
// id. It prefers the "sid" claim on the access token (stashed on
// context by the auth middleware) because the refresh cookie is scoped
// to /auth and is not sent on /me/* requests. Falls back to hashing
// the nf_rt cookie for safety / backwards compatibility with older
// tokens that predate the sid claim.
func currentSessionPublicID(ctx context.Context, deps Deps, plain string) (types.PublicID, bool) {
	if sid, ok := middleware.SessionPublicIDFromContext(ctx); ok {
		return sid, true
	}
	var zero types.PublicID
	if plain == "" {
		return zero, false
	}
	hash := auth.HashOpaque(plain)
	sess, err := deps.Sessions.FindByRefreshHash(ctx, hash)
	if err != nil || sess == nil {
		return zero, false
	}
	return sess.PublicID, true
}
