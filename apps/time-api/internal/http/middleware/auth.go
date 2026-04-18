package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/time-api/internal/errors"
)

// ctxKey is an unexported type used as a context key.
type ctxKey int

const (
	ctxKeyActorUserID ctxKey = iota
	ctxKeySessionIDPublic
	ctxKeyCalendarID
	ctxKeyCalendarIDPublic
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

// WithActor returns a new context carrying the authenticated user's internal id.
func WithActor(ctx context.Context, userID uint32) context.Context {
	return context.WithValue(ctx, ctxKeyActorUserID, userID)
}

// ActorFromContext extracts the authenticated user's internal id.
func ActorFromContext(ctx context.Context) (uint32, bool) {
	v, ok := ctx.Value(ctxKeyActorUserID).(uint32)
	return v, ok
}

// WithSessionPublicID returns a new context carrying the caller's session public id.
func WithSessionPublicID(ctx context.Context, sid types.PublicID) context.Context {
	return context.WithValue(ctx, ctxKeySessionIDPublic, sid)
}

// RequireAuth is an HTTP middleware that resolves the Authorization header,
// authenticates the bearer JWT, and stores the resolved internal user id
// on the request context via WithActor.
func RequireAuth(deps AuthDeps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := bearerFromHeader(r.Header.Get("Authorization"))
			if !ok {
				writeAuthError(w, apierrors.AuthTokenSignatureInvalid)
				return
			}
			if deps.JWT == nil || deps.DB == nil {
				writeAuthError(w, apierrors.AuthTokenSignatureInvalid)
				return
			}
			claims, err := deps.JWT.Verify(tok)
			if err != nil {
				writeAuthError(w, apierrors.AuthTokenSignatureInvalid)
				return
			}
			const q = `SELECT id FROM users WHERE public_id = ? AND enabled = TRUE LIMIT 1`
			var uid uint32
			if err := deps.DB.QueryRowContext(r.Context(), q, claims.UserPublicID).Scan(&uid); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeAuthError(w, apierrors.AuthSessionRevoked)
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Internal error")
				return
			}
			ctx := WithActor(r.Context(), uid)
			var zeroSid types.PublicID
			if claims.SessionPublicID != zeroSid {
				ctx = WithSessionPublicID(ctx, claims.SessionPublicID)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerFromHeader(h string) (string, bool) {
	if h == "" {
		return "", false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}

func writeAuthError(w http.ResponseWriter, spec *apierrors.Spec) {
	writeError(w, spec.Status, spec.Code, spec.Message)
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Code: code, Message: message})
}
