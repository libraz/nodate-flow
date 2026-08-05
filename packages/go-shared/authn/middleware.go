package authn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
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
			details, err := resolveChain(r.Context(), tok, resolvers)
			if err != nil {
				writeJSONResolveError(w, err)
				return
			}
			ctx := WithActor(r.Context(), details.UserID)
			ctx = WithAuthMode(ctx, AuthModeJWT)
			ctx = WithTokenKind(ctx, details.Kind)
			ctx = WithTokenScopes(ctx, details.Scopes)
			if details.WorkspaceID != 0 {
				ctx = WithTokenWorkspaceID(ctx, details.WorkspaceID)
			}
			var zero dbtype.PublicID
			if details.SessionPublicID != zero {
				ctx = WithSessionPublicID(ctx, details.SessionPublicID)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func resolveChain(ctx context.Context, token string, resolvers []TokenResolver) (TokenDetails, error) {
	var lastErr error
	for _, r := range resolvers {
		if r == nil {
			continue
		}
		if detailed, ok := r.(DetailedTokenResolver); ok {
			details, err := detailed.ResolveDetailed(ctx, token)
			if err == nil {
				return details, nil
			}
			lastErr = err
			if !errors.Is(err, ErrTokenInvalid) {
				return TokenDetails{}, err
			}
			continue
		}
		uid, sid, err := r.Resolve(ctx, token)
		if err == nil {
			return TokenDetails{UserID: uid, SessionPublicID: sid, Kind: TokenKindJWT}, nil
		}
		lastErr = err
		// If the resolver recognized the token but it was invalid
		// (e.g. expired PAT), stop trying other resolvers.
		if !errors.Is(err, ErrTokenInvalid) {
			return TokenDetails{}, err
		}
	}
	if lastErr != nil {
		return TokenDetails{}, lastErr
	}
	return TokenDetails{}, ErrTokenInvalid
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
	writeJSONError(w, http.StatusUnauthorized, "AUTH.TOKEN.SIGNATURE_INVALID", "Token signature is invalid")
}

func writeJSONResolveError(w http.ResponseWriter, err error) {
	var ae *apierr.APIError
	if errors.As(err, &ae) && ae.Spec != nil {
		writeJSONError(w, ae.Spec.Status, ae.Spec.Code, ae.Spec.Message)
		return
	}
	if errors.Is(err, ErrUserDisabled) {
		writeJSONError(w, http.StatusUnauthorized, "AUTH.SESSION.UNAUTHORIZED", "You must be signed in to access this resource")
		return
	}
	if errors.Is(err, ErrTokenInvalid) {
		writeJSON401SignatureInvalid(w)
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "INTERNAL.UNEXPECTED", "Unexpected server error")
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{
		Code:    code,
		Message: message,
	})
}
