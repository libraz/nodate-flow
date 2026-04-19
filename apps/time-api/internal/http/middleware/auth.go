package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

// ctxKey is an unexported type used as a context key for
// calendar-specific middleware values. Authentication-level keys
// are managed by the shared authn package.
type ctxKey int

const (
	ctxKeyCalendarID ctxKey = iota
	ctxKeyCalendarIDPublic
	ctxKeySubscription
)

// ACLDB is the minimal subset of *sql.DB that middleware needs.
type ACLDB interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// AuthDeps is the minimal dependency surface used by the auth middleware.
type AuthDeps struct {
	JWT *auth.JWTIssuer
	DB  ACLDB
}

// WithActor delegates to [authn.WithActor].
func WithActor(ctx context.Context, userID uint32) context.Context {
	return authn.WithActor(ctx, userID)
}

// ActorFromContext delegates to [authn.ActorFromContext].
func ActorFromContext(ctx context.Context) (uint32, bool) {
	return authn.ActorFromContext(ctx)
}

// WithSessionPublicID delegates to [authn.WithSessionPublicID].
func WithSessionPublicID(ctx context.Context, sid types.PublicID) context.Context {
	return authn.WithSessionPublicID(ctx, sid)
}

// RequireAuth is an HTTP middleware that resolves the Authorization header,
// authenticates the bearer JWT, and stores the resolved internal user id
// on the request context via [authn.WithActor].
func RequireAuth(deps AuthDeps) func(http.Handler) http.Handler {
	jwtResolver := &authn.JWTResolver{JWT: deps.JWT.JWTIssuer, DB: deps.DB}
	return authn.RequireAuth(jwtResolver)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message})
}
