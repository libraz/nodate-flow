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
	ctxKeyAuthMode
	ctxKeyTokenKind
	ctxKeyTokenScopes
	ctxKeyTokenWorkspaceID
)

// TokenKind identifies the concrete bearer token family that admitted the
// request. It lets downstream middleware enforce token-family-specific
// policies such as PAT scopes without re-parsing the Authorization header.
type TokenKind string

const (
	TokenKindJWT TokenKind = "jwt"
	TokenKindPAT TokenKind = "pat"
	TokenKindMCP TokenKind = "mcp"
)

// AuthMode classifies how the request was authenticated. It is set by
// the auth middlewares and consumed by access-logging middleware and
// service-token-aware handlers that need to branch on whether a real
// user or an internal service issued the request.
type AuthMode string

const (
	// AuthModeJWT identifies requests authenticated through the
	// standard bearer-token chain (JWT, PAT, MCP). The actor user id
	// is populated on the context via [WithActor].
	AuthModeJWT AuthMode = "jwt"
	// AuthModeServiceToken identifies requests authenticated through
	// the flow-worker shared-secret path. No actor user id is set on
	// the context; the request body must specify which workspace the
	// call is scoped to.
	AuthModeServiceToken AuthMode = "service_token"
)

// WithAuthMode returns a new context carrying the authentication mode
// used to admit the request. The auth middleware sets this before
// downstream middleware (LoggerContext) and handlers run.
func WithAuthMode(ctx context.Context, mode AuthMode) context.Context {
	return context.WithValue(ctx, ctxKeyAuthMode, mode)
}

// AuthModeFromContext extracts the authentication mode populated by
// the auth middleware. The boolean is false when no auth middleware
// ran (e.g. public routes).
func AuthModeFromContext(ctx context.Context) (AuthMode, bool) {
	v, ok := ctx.Value(ctxKeyAuthMode).(AuthMode)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// WithTokenKind returns a new context carrying the concrete bearer-token
// family that admitted the request.
func WithTokenKind(ctx context.Context, kind TokenKind) context.Context {
	return context.WithValue(ctx, ctxKeyTokenKind, kind)
}

// TokenKindFromContext extracts the concrete bearer-token family populated
// by the auth middleware.
func TokenKindFromContext(ctx context.Context) (TokenKind, bool) {
	v, ok := ctx.Value(ctxKeyTokenKind).(TokenKind)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// WithTokenScopes returns a new context carrying the granted bearer-token
// scopes. The slice is copied so callers cannot mutate context state after
// storing it.
func WithTokenScopes(ctx context.Context, scopes []string) context.Context {
	cp := append([]string(nil), scopes...)
	return context.WithValue(ctx, ctxKeyTokenScopes, cp)
}

// TokenScopesFromContext extracts granted bearer-token scopes from context.
func TokenScopesFromContext(ctx context.Context) ([]string, bool) {
	v, ok := ctx.Value(ctxKeyTokenScopes).([]string)
	if !ok {
		return nil, false
	}
	return append([]string(nil), v...), true
}

// WithTokenWorkspaceID returns a new context carrying the workspace id an
// opaque bearer token is bound to. JWT sessions do not set this value.
func WithTokenWorkspaceID(ctx context.Context, workspaceID uint32) context.Context {
	return context.WithValue(ctx, ctxKeyTokenWorkspaceID, workspaceID)
}

// TokenWorkspaceIDFromContext extracts the workspace id bound to the bearer
// token that admitted the request. The boolean is false for JWT sessions and
// for token families that are not workspace-bound.
func TokenWorkspaceIDFromContext(ctx context.Context) (uint32, bool) {
	v, ok := ctx.Value(ctxKeyTokenWorkspaceID).(uint32)
	if !ok || v == 0 {
		return 0, false
	}
	return v, true
}

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
