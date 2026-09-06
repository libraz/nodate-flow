package authn

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/problem"
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
			tok, ok := BearerFromHeader(r.Header.Get("Authorization"))
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

// BearerFromHeader parses an Authorization header value and returns the
// bearer token it carries.
//
// This is the one parser for every bearer surface in the product. A
// second copy drifts from this one without anything failing: the drift
// surfaces only as a 401 on a request the operator believes is correct,
// which is the hardest kind of answer to diagnose.
//
// The auth-scheme match is case-insensitive because RFC 7235 defines the
// scheme as a case-insensitive token. Machine clients written against
// other stacks routinely send "bearer", and rejecting those spells the
// difference as an authentication failure.
//
// The token is returned trimmed. Anything comparing it against a
// configured secret MUST trim that secret identically, or a stray space
// in the deployment's own configuration makes the correct client token
// unmatchable while the feature still reports as configured.
func BearerFromHeader(h string) (string, bool) {
	const prefix = "Bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}

// The 401s below are the most-travelled error path in the product:
// this middleware guards nearly every authenticated route, so an
// envelope it gets wrong is the one clients see when a session dies.
// They go through [problem] like every other emitter — see that
// package for why the shape has to match.

func writeJSON401Missing(w http.ResponseWriter) {
	problem.WriteCode(w, http.StatusUnauthorized,
		"AUTH.TOKEN.MISSING_OR_MALFORMED", "Missing or invalid authentication token")
}

func writeJSON401SignatureInvalid(w http.ResponseWriter) {
	problem.WriteCode(w, http.StatusUnauthorized,
		"AUTH.TOKEN.SIGNATURE_INVALID", "Token signature is invalid")
}

func writeJSONResolveError(w http.ResponseWriter, err error) {
	var ae *apierr.APIError
	if errors.As(err, &ae) && ae.Spec != nil {
		// The spec came from a service catalog, so this path can carry
		// description, userAction and the curated i18n key too.
		problem.Write(w, ae.Spec)
		return
	}
	if errors.Is(err, ErrUserDisabled) {
		problem.WriteCode(w, http.StatusUnauthorized,
			"AUTH.SESSION.UNAUTHORIZED", "You must be signed in to access this resource")
		return
	}
	if errors.Is(err, ErrTokenInvalid) {
		writeJSON401SignatureInvalid(w)
		return
	}
	problem.WriteCode(w, http.StatusInternalServerError,
		apierr.CodeInternalUnexpected, "Unexpected server error")
}
