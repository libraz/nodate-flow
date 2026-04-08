package helpers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	authhandlers "github.com/nodate-flow/nodate-flow/apps/api/internal/http/handlers/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/middleware"
)

// TestServer is a running httptest.Server bound to the same Huma+chi
// route set as cmd/api/main.go, but wired to a real database handle so
// integration tests can drive the API end to end.
type TestServer struct {
	BaseURL string
	Server  *httptest.Server
	DB      *sql.DB
}

// StartTestServer boots an httptest.Server that mounts the same route
// set as cmd/api/main.go against the supplied *sql.DB. The server is
// torn down via t.Cleanup.
func StartTestServer(t *testing.T, db *sql.DB) *TestServer {
	t.Helper()
	require.NotNil(t, db, "StartTestServer requires a non-nil *sql.DB")

	queries := generated.New(db)

	jwtIssuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	require.NoError(t, err, "init jwt issuer")

	router := chi.NewRouter()

	// Apply RequireAuth before huma routes are registered so that all
	// chi-mounted handlers see the middleware. The middleware itself
	// short-circuits for the public auth endpoints; everything else
	// needs a valid bearer token.
	authMW := middleware.RequireAuth(middleware.AuthDeps{
		JWT:     jwtIssuer,
		Queries: queries,
		DB:      passthroughDB{db},
	})
	router.Use(skipPublicPaths(authMW, publicPaths))

	api := humachi.New(router, huma.DefaultConfig("nodate-flow-test", "0.0.0"))

	// Health endpoint mirrors main.go.
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Health check",
	}, func(_ context.Context, _ *struct{}) (*healthOutput, error) {
		out := &healthOutput{}
		out.Body.Status = "ok"
		return out, nil
	})

	deps := authhandlers.Deps{DB: db, Queries: queries, JWT: jwtIssuer}
	registerAuthRoutes(api, deps)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &TestServer{BaseURL: srv.URL, Server: srv, DB: db}
}

type healthOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

// publicPaths lists URL paths that must NOT require a bearer token.
// Everything else routed through the chi router is protected by
// RequireAuth.
var publicPaths = []string{
	"/health",
	"/auth/register",
	"/auth/login",
	"/auth/oidc/google/start",
	"/auth/oidc/google/callback",
	"/auth/refresh",
	"/auth/logout",
}

// skipPublicPaths wraps a middleware so that requests to publicPaths
// bypass it entirely. This keeps unauthenticated endpoints accessible
// while still letting RequireAuth guard /me and any future protected
// route via a single chi.Use call.
func skipPublicPaths(mw func(http.Handler) http.Handler, paths []string) func(http.Handler) http.Handler {
	set := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		set[p] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		wrapped := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if _, ok := set[path]; ok {
				next.ServeHTTP(w, r)
				return
			}
			// OpenAPI / docs paths are also public.
			if path == "/openapi.json" || path == "/openapi.yaml" || strings.HasPrefix(path, "/docs") {
				next.ServeHTTP(w, r)
				return
			}
			wrapped.ServeHTTP(w, r)
		})
	}
}

// registerAuthRoutes registers the same auth operations as
// cmd/api/main.go's registerAuthRoutes. We duplicate the wiring rather
// than import main so the test server can apply middleware in the
// correct order.
func registerAuthRoutes(api huma.API, deps authhandlers.Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-register",
		Method:      http.MethodPost,
		Path:        "/auth/register",
		Summary:     "Register a new local-password account",
	}, authhandlers.Register(deps))

	huma.Register(api, huma.Operation{
		OperationID: "auth-login",
		Method:      http.MethodPost,
		Path:        "/auth/login",
		Summary:     "Log in with email and password",
	}, authhandlers.Login(deps))

	huma.Register(api, huma.Operation{
		OperationID: "auth-oidc-google-start",
		Method:      http.MethodGet,
		Path:        "/auth/oidc/google/start",
		Summary:     "Start a Google OIDC login flow",
	}, authhandlers.OIDCGoogleStart(deps))

	huma.Register(api, huma.Operation{
		OperationID: "auth-oidc-google-callback",
		Method:      http.MethodGet,
		Path:        "/auth/oidc/google/callback",
		Summary:     "Complete a Google OIDC login flow",
	}, authhandlers.OIDCGoogleCallback(deps))

	huma.Register(api, huma.Operation{
		OperationID: "auth-refresh",
		Method:      http.MethodPost,
		Path:        "/auth/refresh",
		Summary:     "Rotate refresh token and issue a new access token",
	}, authhandlers.Refresh(deps))

	huma.Register(api, huma.Operation{
		OperationID: "auth-logout",
		Method:      http.MethodPost,
		Path:        "/auth/logout",
		Summary:     "Revoke a session",
	}, authhandlers.Logout(deps))

	huma.Register(api, huma.Operation{
		OperationID: "me",
		Method:      http.MethodGet,
		Path:        "/me",
		Summary:     "Return the authenticated user's profile",
	}, authhandlers.Me(deps))
}

// passthroughDB adapts *sql.DB to the middleware.ACLDB interface.
type passthroughDB struct{ db *sql.DB }

func (p passthroughDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return p.db.QueryRowContext(ctx, query, args...)
}
