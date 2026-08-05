package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/packages/go-shared/authn"
)

// decodeProblemType reads the RFC 9457 `type` (the canonical error code)
// out of a recorded response body.
func decodeProblemType(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var p problemBodyDecoded
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))
	return p.Type
}

func TestTokenWorkspaceScopeFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		pattern string
		want    TokenWorkspaceScope
	}{
		{"/workspaces/{wsId}/labels", TokenWorkspaceScopeAnchored},
		{"/workspaces/{wsId}/calendars/{calId}/events", TokenWorkspaceScopeAnchored},
		{"/tasks", TokenWorkspaceScopeDerived},
		{"/tasks/reorder", TokenWorkspaceScopeDerived},
		{"/tasks/{id}/comments", TokenWorkspaceScopeDerived},
		{"/projects/{prjId}/members", TokenWorkspaceScopeDerived},
		{"/signals", TokenWorkspaceScopeDerived},
		{"/me/tasks", TokenWorkspaceScopeCrossWorkspace},
		{"/me/tasks-with-dates", TokenWorkspaceScopeCrossWorkspace},
		{"/inbox/{id}/archive", TokenWorkspaceScopeCrossWorkspace},
		{"/relation-suggestions/{suggestionId}/resolve", TokenWorkspaceScopeCrossWorkspace},
		// An unrouted request carries no pattern and must fail closed.
		{"", TokenWorkspaceScopeCrossWorkspace},
	}
	for _, tc := range cases {
		require.Equalf(t, tc.want, TokenWorkspaceScopeFor(tc.pattern),
			"unexpected scope for %q", tc.pattern)
	}
}

// requestWithRoutePattern builds a request whose chi route context reports
// the given matched pattern, mimicking what the router populates before a
// group middleware runs.
func requestWithRoutePattern(ctx context.Context, method, pattern string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.RoutePatterns = append(rctx.RoutePatterns, pattern)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return httptest.NewRequest(method, "http://example.test/", nil).WithContext(ctx)
}

// TestRequireTokenWorkspaceBindingRejectsCrossWorkspaceRoutes locks in that a
// workspace-bound bearer token cannot reach the routes that deliberately span
// every workspace the caller belongs to. Those routes have no workspace to
// compare the binding against, so honouring the token there would silently
// widen it into a full-account credential.
func TestRequireTokenWorkspaceBindingRejectsCrossWorkspaceRoutes(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RequireTokenWorkspaceBinding(nil)(next)

	ctx := authn.WithTokenKind(context.Background(), authn.TokenKindPAT)
	ctx = authn.WithTokenWorkspaceID(ctx, 7)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, requestWithRoutePattern(ctx, http.MethodGet, "/me/tasks"))
	require.Equal(t, http.StatusForbidden, rr.Code)
	require.Equal(t, "WS.WORKSPACE.ACCESS_DENIED", decodeProblemType(t, rr))
}

// TestRequireTokenWorkspaceBindingMCPErrorCode verifies MCP tokens get the
// MCP-specific code so an MCP client can tell a binding failure apart from a
// plain membership failure.
func TestRequireTokenWorkspaceBindingMCPErrorCode(t *testing.T) {
	t.Parallel()

	handler := RequireTokenWorkspaceBinding(nil)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	ctx := authn.WithTokenKind(context.Background(), authn.TokenKindMCP)
	ctx = authn.WithTokenWorkspaceID(ctx, 7)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, requestWithRoutePattern(ctx, http.MethodGet, "/inbox"))
	require.Equal(t, http.StatusForbidden, rr.Code)
	require.Equal(t, "MCP.TOKEN.WORKSPACE_MISMATCH", decodeProblemType(t, rr))
}

// TestRequireTokenWorkspaceBindingPassesUnboundAndDerivedRoutes verifies the
// two paths that must not be blocked here: session JWTs (no binding at all)
// and routes whose workspace is resolved downstream, where
// acl.CheckWorkspaceMember performs the comparison.
func TestRequireTokenWorkspaceBindingPassesUnboundAndDerivedRoutes(t *testing.T) {
	t.Parallel()

	handler := RequireTokenWorkspaceBinding(nil)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	// No binding: a browser session must be untouched even on a
	// cross-workspace route.
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, requestWithRoutePattern(context.Background(), http.MethodGet, "/me/tasks"))
	require.Equal(t, http.StatusOK, rr.Code)

	// Bound token on a route whose workspace comes from the task id.
	ctx := authn.WithTokenKind(context.Background(), authn.TokenKindPAT)
	ctx = authn.WithTokenWorkspaceID(ctx, 7)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, requestWithRoutePattern(ctx, http.MethodGet, "/tasks/{id}"))
	require.Equal(t, http.StatusOK, rr.Code)
}

// TestRequireWorkspaceRoleForWrites locks in the guest contract: reads pass at
// any role, mutations need at least the configured role, and a mutation that
// arrives without a resolved workspace fails closed.
func TestRequireWorkspaceRoleForWrites(t *testing.T) {
	t.Parallel()

	handler := RequireWorkspaceRoleForWrites(WorkspaceRoleMember)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	withRole := func(role WorkspaceRole) context.Context {
		ctx := context.WithValue(context.Background(), ctxKeyWorkspaceID, uint32(7))
		return context.WithValue(ctx, ctxKeyWorkspaceRole, role)
	}

	cases := []struct {
		name   string
		method string
		ctx    context.Context
		want   int
	}{
		{"guest read", http.MethodGet, withRole(WorkspaceRoleGuest), http.StatusOK},
		{"guest write", http.MethodPatch, withRole(WorkspaceRoleGuest), http.StatusForbidden},
		{"guest delete", http.MethodDelete, withRole(WorkspaceRoleGuest), http.StatusForbidden},
		{"member write", http.MethodPost, withRole(WorkspaceRoleMember), http.StatusOK},
		{"owner write", http.MethodPost, withRole(WorkspaceRoleOwner), http.StatusOK},
		{"write without workspace context", http.MethodPost, context.Background(), http.StatusForbidden},
		{"read without workspace context", http.MethodGet, context.Background(), http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, "http://example.test/", nil).WithContext(tc.ctx)
			handler.ServeHTTP(rr, req)
			require.Equal(t, tc.want, rr.Code)
		})
	}
}
