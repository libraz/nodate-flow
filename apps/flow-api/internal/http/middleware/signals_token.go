package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// AuthMode re-exports [authn.AuthMode] so handlers and tests inside
// flow-api can branch on the authentication mode without importing the
// shared package directly.
type AuthMode = authn.AuthMode

// AuthModeJWT and AuthModeServiceToken re-export the two values that
// [AuthMode] is allowed to take.
const (
	AuthModeJWT          = authn.AuthModeJWT
	AuthModeServiceToken = authn.AuthModeServiceToken
)

// AuthModeFromContext re-exports [authn.AuthModeFromContext] so
// handlers can read the auth mode without importing the shared
// authn package.
func AuthModeFromContext(ctx context.Context) (AuthMode, bool) {
	return authn.AuthModeFromContext(ctx)
}

// RequireSignalsAuth returns an HTTP middleware that admits requests to
// the /signals collection via either of two paths:
//
//  1. The flow-api signal token (constant-time bearer comparison).
//     This is intended for trusted internal callers such as the
//     scheduled flow-worker binary that emits signals from cron-style
//     loops, or the presence-discord gateway. The token is configured
//     via NF_FLOW_API_SIGNAL_TOKEN and verified once per request using
//     [crypto/subtle.ConstantTimeCompare] so an unequal length or
//     mismatched byte does not leak through a timing side channel.
//  2. The standard authenticated chain (JWT / PAT / MCP) wired by the
//     supplied jwtMW. This middleware delegates to jwtMW whenever the
//     bearer token does not match the configured service token so that
//     real users can still call /signals.
//
// When serviceToken is empty the middleware is a thin passthrough to
// jwtMW: the service-token path is never admitted, so the route still
// requires a valid bearer JWT (or PAT / MCP). This is the safe default
// for deployments that have not opted in to the worker binary.
//
// On the service-token path the middleware:
//   - sets authn.AuthModeServiceToken on the request context so the
//     access log can distinguish service calls from user calls, and
//     handlers can branch (e.g. skip workspace-member checks that
//     depend on an actor user id);
//   - does NOT populate an actor user id — service-token requests are
//     anonymous to the audit log and must specify their workspace
//     scope in the request body;
//   - does NOT call jwtMW, so the chain short-circuits without
//     touching the database or attempting JWT verification.
//
// On the user-token path the middleware simply delegates to jwtMW,
// which populates the actor user id, session public id, and
// authn.AuthModeJWT on the context.
//
// SECURITY. The Authorization header is inspected via a single
// strings.HasPrefix("Bearer ") + length check followed by a constant-
// time byte comparison. Empty or missing Authorization headers fall
// through to jwtMW (which returns 401). A request that presents the
// correct service token receives no actor and is permitted only to
// reach handlers that have been audited for the service-token path.
// This middleware MUST NOT be attached to any router group other than
// the /signals collection.
func RequireSignalsAuth(jwtMW func(http.Handler) http.Handler, serviceToken string) func(http.Handler) http.Handler {
	// Snapshot the configured token as a byte slice once at construction
	// time so the per-request hot path stays allocation-free.
	expected := []byte(serviceToken)
	enabled := len(expected) > 0

	return func(next http.Handler) http.Handler {
		// Always compose the JWT path so deployments without a service
		// token fall through to standard auth unchanged.
		jwtNext := jwtMW(next)
		if !enabled {
			return jwtNext
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := bearerFromAuthHeader(r.Header.Get("Authorization"))
			// Missing / malformed Authorization → fall through to the
			// JWT chain so it can emit its standard 401 envelope.
			if !ok {
				jwtNext.ServeHTTP(w, r)
				return
			}

			// Constant-time comparison. ConstantTimeCompare returns 0 on
			// any difference (including length mismatch), so a short
			// guess cannot probe the configured token's length.
			if subtle.ConstantTimeCompare([]byte(tok), expected) == 1 {
				ctx := authn.WithAuthMode(r.Context(), authn.AuthModeServiceToken)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// Bearer was present but did not match the service token.
			// Hand off to the JWT chain so a real user with a valid
			// bearer is still admitted, and any other input fails with
			// the standard AUTH.TOKEN.SIGNATURE_INVALID 401.
			jwtNext.ServeHTTP(w, r)
		})
	}
}

// RequireServiceTokenOnly returns an HTTP middleware that admits a
// request only when its Authorization header presents the configured
// service token (constant-time compared). Unlike [RequireSignalsAuth]
// there is NO JWT fallback — a missing, malformed, or non-matching
// bearer always 401s, even if the same value would be a valid JWT /
// PAT / MCP token on another route. The middleware is the auth guard
// for the /internal/* route group, which carries endpoints meant to be
// reachable only by other backend processes (flow-worker,
// presence-discord) and never by a logged-in user session.
//
// When serviceToken is empty the middleware rejects every request:
// /internal/* exists only to serve the operator-configured service
// callers, so leaving the secret unset MUST close the group entirely
// rather than admitting anyone. The 401 envelope mirrors the one
// produced by [authn.RequireAuth] (AUTH.TOKEN.MISSING_OR_MALFORMED /
// AUTH.TOKEN.SIGNATURE_INVALID) so an attacker probing the endpoint
// cannot distinguish "wrong token" from "no token configured" from
// "JWT-only endpoint" by the response shape.
//
// On the service-token path the middleware sets
// [authn.AuthModeServiceToken] on the request context so handlers and
// the access log can branch on the auth mode the same way they do for
// /signals service-token requests. No actor user id is populated;
// /internal/* handlers must not depend on one.
//
// SECURITY. The Authorization header parser and the constant-time
// comparison are identical to those used by [RequireSignalsAuth];
// timing side channels and length probes are both blocked.
func RequireServiceTokenOnly(serviceToken string) func(http.Handler) http.Handler {
	expected := []byte(serviceToken)
	enabled := len(expected) > 0

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled {
				writeServiceTokenMissing(w)
				return
			}
			tok, ok := bearerFromAuthHeader(r.Header.Get("Authorization"))
			if !ok {
				writeServiceTokenMissing(w)
				return
			}
			if subtle.ConstantTimeCompare([]byte(tok), expected) != 1 {
				writeServiceTokenInvalid(w)
				return
			}
			ctx := authn.WithAuthMode(r.Context(), authn.AuthModeServiceToken)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// writeServiceTokenMissing writes the canonical
// AUTH.TOKEN.MISSING_OR_MALFORMED 401 envelope. Kept here so the
// /internal/* route group does not need to import the authn package
// directly to share the wire shape.
func writeServiceTokenMissing(w http.ResponseWriter) {
	writeAuth401(w, "AUTH.TOKEN.MISSING_OR_MALFORMED", "Missing or invalid authentication token")
}

// writeServiceTokenInvalid writes the canonical
// AUTH.TOKEN.SIGNATURE_INVALID 401 envelope.
func writeServiceTokenInvalid(w http.ResponseWriter) {
	writeAuth401(w, "AUTH.TOKEN.SIGNATURE_INVALID", "Token signature is invalid")
}

// writeAuth401 encodes a minimal {code, message} JSON envelope at HTTP
// 401. The shape matches [authn.RequireAuth]'s 401 response so the
// /internal/* group is indistinguishable from a standard auth-guarded
// route on the wire.
func writeAuth401(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	// Hand-rolled JSON keeps this middleware free of an encoding/json
	// dependency and avoids the per-request allocation of a struct.
	// The two values are constants drawn from the error catalog so no
	// escaping is needed.
	_, _ = w.Write([]byte(`{"code":"` + code + `","message":"` + message + `"}`))
}

// bearerFromAuthHeader parses an HTTP Authorization header value and
// returns the bearer token portion. It mirrors the parser used by
// [authn.RequireAuth] but is duplicated here so the middleware can
// stay free of unexported symbols from the shared package.
func bearerFromAuthHeader(h string) (string, bool) {
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
