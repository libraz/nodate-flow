package middleware

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
)

// AuthDeps is the minimal dependency surface used by the auth middleware.
type AuthDeps struct {
	JWT     *auth.JWTIssuer
	Queries *generated.Queries
	// DB is used for the JWT → internal user id resolution.
	DB ACLDB
}

// RequireAuth is an HTTP middleware that resolves the Authorization header,
// authenticates the bearer token (JWT, pat_, or mcp_), and stores the
// resolved internal user id on the request context via [authn.WithActor].
//
// It delegates JWT resolution to the shared [authn.JWTResolver] and
// handles PAT/MCP tokens locally (they depend on flow-api-specific
// sqlc queries).
func RequireAuth(deps AuthDeps) func(http.Handler) http.Handler {
	// Build the resolver chain: JWT first, then PAT, then MCP.
	jwtResolver := &authn.JWTResolver{JWT: deps.JWT.JWTIssuer, DB: deps.DB}
	patResolver := &patResolver{queries: deps.Queries}
	mcpResolver := &mcpResolver{queries: deps.Queries}

	return authn.RequireAuth(jwtResolver, patResolver, mcpResolver)
}

// patResolver resolves personal access tokens (pat_ prefix).
type patResolver struct {
	queries *generated.Queries
}

// Resolve validates a PAT token and returns the associated user id.
func (r *patResolver) Resolve(ctx context.Context, token string) (uint32, dbtype.PublicID, error) {
	if !strings.HasPrefix(token, authn.PrefixPAT) {
		return 0, dbtype.PublicID{}, authn.ErrTokenInvalid
	}
	hash := authn.HashOpaque(token)
	row, err := r.queries.FindPatByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, dbtype.PublicID{}, apierrors.New(apierrors.AuthPatTokenUnknown)
		}
		return 0, dbtype.PublicID{}, err
	}
	if row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now()) {
		return 0, dbtype.PublicID{}, apierrors.New(apierrors.AuthPatExpired)
	}
	return row.UserID, dbtype.PublicID{}, nil
}

// mcpResolver resolves MCP bearer tokens (mcp_ prefix).
type mcpResolver struct {
	queries *generated.Queries
}

// Resolve validates an MCP token and returns the associated user id.
func (r *mcpResolver) Resolve(ctx context.Context, token string) (uint32, dbtype.PublicID, error) {
	if !strings.HasPrefix(token, authn.PrefixMCP) {
		return 0, dbtype.PublicID{}, authn.ErrTokenInvalid
	}
	hash := authn.HashOpaque(token)
	row, err := r.queries.FindUserForMcpToken(ctx, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, dbtype.PublicID{}, apierrors.New(apierrors.AuthPatTokenUnknown)
		}
		return 0, dbtype.PublicID{}, err
	}
	if row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now()) {
		return 0, dbtype.PublicID{}, apierrors.New(apierrors.AuthPatExpired)
	}
	return row.UserID, dbtype.PublicID{}, nil
}
