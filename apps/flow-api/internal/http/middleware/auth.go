package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
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

// RequireBearerTokenScope enforces coarse read/write scopes for opaque API
// tokens. Browser/user JWT sessions are not scoped here; PAT and MCP bearer
// tokens must carry read:workspace for safe methods and write:workspace for
// mutating methods. write:workspace implies read:workspace, matching MCP tool
// scope widening.
func RequireBearerTokenScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, ok := authn.TokenKindFromContext(r.Context())
		if !ok || (kind != authn.TokenKindPAT && kind != authn.TokenKindMCP) {
			next.ServeHTTP(w, r)
			return
		}
		required := requiredScopeForMethod(r.Method)
		if required == "" {
			next.ServeHTTP(w, r)
			return
		}
		scopes, _ := authn.TokenScopesFromContext(r.Context())
		if tokenHasScope(scopes, required) {
			next.ServeHTTP(w, r)
			return
		}
		if kind == authn.TokenKindMCP {
			writeSpecError(w, apierrors.McpScopeInsufficient)
			return
		}
		writeSpecError(w, apierrors.AuthPatScopeInsufficient)
	})
}

func requiredScopeForMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return "read:workspace"
	default:
		return "write:workspace"
	}
}

func tokenHasScope(scopes []string, required string) bool {
	for _, scope := range scopes {
		switch strings.TrimSpace(scope) {
		case required, "*", "admin":
			return true
		case "write:workspace":
			if required == "read:workspace" {
				return true
			}
		case "write":
			if required == "read:workspace" || required == "write:workspace" {
				return true
			}
		case "read":
			if required == "read:workspace" {
				return true
			}
		}
	}
	return false
}

func parseBearerScopes(raw []byte) []string {
	var values []string
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// patResolver resolves personal access tokens (pat_ prefix).
type patResolver struct {
	queries *generated.Queries
}

// Resolve validates a PAT token and returns the associated user id.
func (r *patResolver) Resolve(ctx context.Context, token string) (uint32, dbtype.PublicID, error) {
	details, err := r.ResolveDetailed(ctx, token)
	return details.UserID, details.SessionPublicID, err
}

// ResolveDetailed validates a PAT token and returns the associated user id
// plus the granted API scopes.
func (r *patResolver) ResolveDetailed(ctx context.Context, token string) (authn.TokenDetails, error) {
	if !strings.HasPrefix(token, authn.PrefixPAT) {
		return authn.TokenDetails{}, authn.ErrTokenInvalid
	}
	hash := authn.HashOpaque(token)
	row, err := r.queries.FindPatByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authn.TokenDetails{}, apierrors.New(apierrors.AuthPatTokenUnknown)
		}
		return authn.TokenDetails{}, err
	}
	if row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now()) {
		return authn.TokenDetails{}, apierrors.New(apierrors.AuthPatExpired)
	}
	return authn.TokenDetails{
		UserID:      row.UserID,
		Kind:        authn.TokenKindPAT,
		Scopes:      parseBearerScopes(row.ScopesJson),
		WorkspaceID: row.WorkspaceID,
	}, nil
}

// mcpResolver resolves MCP bearer tokens (mcp_ prefix).
type mcpResolver struct {
	queries *generated.Queries
}

// Resolve validates an MCP token and returns the associated user id.
func (r *mcpResolver) Resolve(ctx context.Context, token string) (uint32, dbtype.PublicID, error) {
	details, err := r.ResolveDetailed(ctx, token)
	return details.UserID, details.SessionPublicID, err
}

// ResolveDetailed validates an MCP token and returns the associated user id
// plus its granted scopes for REST fallback enforcement.
func (r *mcpResolver) ResolveDetailed(ctx context.Context, token string) (authn.TokenDetails, error) {
	if !strings.HasPrefix(token, authn.PrefixMCP) {
		return authn.TokenDetails{}, authn.ErrTokenInvalid
	}
	hash := authn.HashOpaque(token)
	row, err := r.queries.FindUserForMcpToken(ctx, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authn.TokenDetails{}, apierrors.New(apierrors.AuthPatTokenUnknown)
		}
		return authn.TokenDetails{}, err
	}
	if row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now()) {
		return authn.TokenDetails{}, apierrors.New(apierrors.AuthPatExpired)
	}
	return authn.TokenDetails{
		UserID:      row.UserID,
		Kind:        authn.TokenKindMCP,
		Scopes:      parseBearerScopes(row.ScopesJson),
		WorkspaceID: row.WorkspaceID,
	}, nil
}
