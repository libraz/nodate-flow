package authn

import (
	"context"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// ctxKey is an unexported type used as a context key to avoid collisions
// with other packages storing values in the request context.
type ctxKey int

const (
	ctxKeyActorUserID ctxKey = iota
	ctxKeySessionPublicID
	ctxKeyClientIP
)

// WithActor returns a new context carrying the authenticated user's
// internal numeric id. This is normally called by [RequireAuth] before
// any downstream middleware or handler runs.
func WithActor(ctx context.Context, userID uint32) context.Context {
	return context.WithValue(ctx, ctxKeyActorUserID, userID)
}

// ActorFromContext extracts the authenticated user's internal id. The
// boolean is false when no auth middleware has populated the context.
func ActorFromContext(ctx context.Context) (uint32, bool) {
	v, ok := ctx.Value(ctxKeyActorUserID).(uint32)
	return v, ok
}

// WithSessionPublicID returns a new context carrying the caller's
// session public id, resolved from the "sid" claim on the access token.
func WithSessionPublicID(ctx context.Context, sid dbtype.PublicID) context.Context {
	return context.WithValue(ctx, ctxKeySessionPublicID, sid)
}

// SessionPublicIDFromContext extracts the caller's session public id as
// populated by [RequireAuth]. The boolean is false when no session id
// was present on the access token (e.g. PAT/MCP tokens).
func SessionPublicIDFromContext(ctx context.Context) (dbtype.PublicID, bool) {
	v, ok := ctx.Value(ctxKeySessionPublicID).(dbtype.PublicID)
	if !ok {
		return dbtype.PublicID{}, false
	}
	var zero dbtype.PublicID
	if v == zero {
		return zero, false
	}
	return v, true
}

// WithClientIP returns a new context carrying the caller's client IP
// address (already normalized by the ClientIP middleware).
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKeyClientIP, ip)
}

// ClientIPFromContext extracts the caller's client IP address as
// populated by the ClientIP middleware. Returns an empty string when
// no middleware has populated the context.
func ClientIPFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyClientIP).(string)
	return v
}
