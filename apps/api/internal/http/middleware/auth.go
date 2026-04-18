package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	stddb "database/sql"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/api/internal/errors"
)

// AuthDeps is the minimal dependency surface used by the auth middleware.
type AuthDeps struct {
	JWT     *auth.JWTIssuer
	Queries *generated.Queries
	// DB is used for the JWT → internal user id resolution, which is
	// not currently exposed through a generated query.
	DB ACLDB
}

// RequireAuth is an HTTP middleware that resolves the Authorization header,
// authenticates the bearer token (JWT, pat_, or mcp_), and stores the
// resolved internal user id on the request context via WithActor.
//
// On failure it writes the canonical JSON error envelope with 401 and the
// AUTH.* error code most appropriate for the situation.
func RequireAuth(deps AuthDeps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, ok := bearerFromHeader(r.Header.Get("Authorization"))
			if !ok {
				writeAuthError(w, apierrors.AuthTokenSignatureInvalid)
				return
			}
			userID, sid, err := resolveBearer(r.Context(), deps, tok)
			if err != nil {
				var ae *apierrors.APIError
				if errors.As(err, &ae) {
					writeAuthError(w, ae.Spec)
					return
				}
				writeAuthError(w, apierrors.InternalUnexpected)
				return
			}
			ctx := WithActor(r.Context(), userID)
			var zeroSid types.PublicID
			if sid != zeroSid {
				ctx = WithSessionPublicID(ctx, sid)
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

func resolveBearer(ctx context.Context, deps AuthDeps, tok string) (uint32, types.PublicID, error) {
	switch {
	case strings.HasPrefix(tok, auth.PrefixPAT):
		uid, err := resolvePAT(ctx, deps, tok)
		return uid, types.PublicID{}, err
	case strings.HasPrefix(tok, auth.PrefixMCP):
		uid, err := resolveMCP(ctx, deps, tok)
		return uid, types.PublicID{}, err
	default:
		return resolveJWT(ctx, deps, tok)
	}
}

func resolveJWT(ctx context.Context, deps AuthDeps, tok string) (uint32, types.PublicID, error) {
	if deps.JWT == nil || deps.DB == nil {
		return 0, types.PublicID{}, apierrors.New(apierrors.AuthTokenSignatureInvalid)
	}
	claims, err := deps.JWT.Verify(tok)
	if err != nil {
		return 0, types.PublicID{}, apierrors.New(apierrors.AuthTokenSignatureInvalid)
	}
	const q = `SELECT id FROM users WHERE public_id = ? AND enabled = TRUE LIMIT 1`
	var uid uint32
	if err := deps.DB.QueryRowContext(ctx, q, claims.UserPublicID).Scan(&uid); err != nil {
		if errors.Is(err, stddb.ErrNoRows) {
			return 0, types.PublicID{}, apierrors.New(apierrors.AuthSessionRevoked)
		}
		return 0, types.PublicID{}, err
	}
	return uid, claims.SessionPublicID, nil
}

func resolvePAT(ctx context.Context, deps AuthDeps, tok string) (uint32, error) {
	hash := auth.HashOpaque(tok)
	row, err := deps.Queries.FindPatByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, stddb.ErrNoRows) {
			return 0, apierrors.New(apierrors.AuthPatTokenUnknown)
		}
		return 0, err
	}
	if row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now()) {
		return 0, apierrors.New(apierrors.AuthPatExpired)
	}
	return row.UserID, nil
}

func resolveMCP(ctx context.Context, deps AuthDeps, tok string) (uint32, error) {
	hash := auth.HashOpaque(tok)
	row, err := deps.Queries.FindUserForMcpToken(ctx, hash)
	if err != nil {
		if errors.Is(err, stddb.ErrNoRows) {
			return 0, apierrors.New(apierrors.AuthPatTokenUnknown)
		}
		return 0, err
	}
	if row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now()) {
		return 0, apierrors.New(apierrors.AuthPatExpired)
	}
	return row.UserID, nil
}

func writeAuthError(w http.ResponseWriter, spec *apierrors.Spec) {
	writeError(w, spec.Status, spec.Code, spec.Message)
}
