package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
)

func TestRequireBearerTokenScope_PATReadCannotWrite(t *testing.T) {
	t.Parallel()

	rec := serveBearerScope(t, authn.TokenKindPAT, []string{"read:workspace"}, http.MethodPatch)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PATCH with read-only PAT status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	var body problemBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Type != "AUTH.PAT.SCOPE_INSUFFICIENT" {
		t.Fatalf("error type = %q, want AUTH.PAT.SCOPE_INSUFFICIENT", body.Type)
	}
}

func TestRequireBearerTokenScope_PATWriteCanReadAndWrite(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := serveBearerScope(t, authn.TokenKindPAT, []string{"write:workspace"}, method)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s with write PAT status = %d, want %d", method, rec.Code, http.StatusNoContent)
		}
	}
}

func TestRequireBearerTokenScope_JWTBypassesPATScopes(t *testing.T) {
	t.Parallel()

	rec := serveBearerScope(t, authn.TokenKindJWT, nil, http.MethodDelete)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE with JWT status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestRequireBearerTokenScope_MCPUsesMCPScopeError(t *testing.T) {
	t.Parallel()

	rec := serveBearerScope(t, authn.TokenKindMCP, []string{"read:workspace"}, http.MethodPost)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST with read-only MCP token status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	var body problemBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Type != "MCP.SCOPE.INSUFFICIENT" {
		t.Fatalf("error type = %q, want MCP.SCOPE.INSUFFICIENT", body.Type)
	}
}

func TestParseBearerScopes(t *testing.T) {
	t.Parallel()

	got := parseBearerScopes([]byte(`[" read:workspace ","","write:workspace"]`))
	want := []string{"read:workspace", "write:workspace"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scope[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got := parseBearerScopes([]byte(`{"bad":true}`)); got != nil {
		t.Fatalf("malformed scopes = %#v, want nil", got)
	}
}

func serveBearerScope(t *testing.T, kind authn.TokenKind, scopes []string, method string) *httptest.ResponseRecorder {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(method, "/test", nil)
	ctx := authn.WithTokenKind(req.Context(), kind)
	ctx = authn.WithTokenScopes(ctx, scopes)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	RequireBearerTokenScope(next).ServeHTTP(rec, req)
	return rec
}
