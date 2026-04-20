package authn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// RequireAuth returns an HTTP middleware that extracts the Bearer token
// from the Authorization header, tries each resolver in order until one
// succeeds, and stores the authenticated user id and session public id
// on the request context via [WithActor] and [WithSessionPublicID].
//
// When no resolver succeeds the middleware writes a 401 JSON error
// response and does not call the next handler.
//
// Resolvers are tried in declaration order: the first non-nil resolver
// whose Resolve method does not return [ErrTokenInvalid] wins. This
// allows callers to compose JWT, PAT, and MCP resolvers in a single
// chain.
func RequireAuth(resolvers ...TokenResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := bearerFromHeader(r.Header.Get("Authorization"))
			if !ok {
				writeJSON401Missing(w)
				return
			}
			userID, sid, err := resolveChain(r.Context(), tok, resolvers)
			if err != nil {
				writeJSON401SignatureInvalid(w)
				return
			}
			ctx := WithActor(r.Context(), userID)
			var zero dbtype.PublicID
			if sid != zero {
				ctx = WithSessionPublicID(ctx, sid)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func resolveChain(ctx context.Context, token string, resolvers []TokenResolver) (uint32, dbtype.PublicID, error) {
	var lastErr error
	for _, r := range resolvers {
		if r == nil {
			continue
		}
		uid, sid, err := r.Resolve(ctx, token)
		if err == nil {
			return uid, sid, nil
		}
		lastErr = err
		// If the resolver recognized the token but it was invalid
		// (e.g. expired PAT), stop trying other resolvers.
		if !errors.Is(err, ErrTokenInvalid) {
			return 0, dbtype.PublicID{}, err
		}
	}
	if lastErr != nil {
		return 0, dbtype.PublicID{}, lastErr
	}
	return 0, dbtype.PublicID{}, ErrTokenInvalid
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

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON401Missing(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(errorBody{
		Code:    "AUTH.TOKEN.MISSING_OR_MALFORMED",
		Message: "Missing or invalid authentication token",
	})
}

func writeJSON401SignatureInvalid(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(errorBody{
		Code:    "AUTH.TOKEN.SIGNATURE_INVALID",
		Message: "Token signature is invalid",
	})
}
