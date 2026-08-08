package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// opSecurity indexes the security requirement each operation declares in
// the OpenAPI documents the router produces, keyed by "METHOD path".
func opSecurity(t *testing.T, apis []huma.API) map[string][]map[string][]string {
	t.Helper()
	out := map[string][]map[string][]string{}
	for _, api := range apis {
		spec := api.OpenAPI()
		if spec == nil || spec.Paths == nil {
			continue
		}
		for path, item := range spec.Paths {
			for method, op := range map[string]*huma.Operation{
				http.MethodGet:    item.Get,
				http.MethodPost:   item.Post,
				http.MethodPut:    item.Put,
				http.MethodPatch:  item.Patch,
				http.MethodDelete: item.Delete,
			} {
				if op == nil {
					continue
				}
				out[method+" "+path] = op.Security
			}
		}
	}
	return out
}

// TestSpecDeclaresTheBearerScheme pins the one thing a reader of the
// published document needs in order to call anything: a named
// authentication mechanism. Without it the bundled reference UI has no
// way to send a token and a generated client has no way to accept one.
func TestSpecDeclaresTheBearerScheme(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	if len(res.APIs) == 0 {
		t.Fatal("router registered no APIs")
	}
	for _, api := range res.APIs {
		spec := api.OpenAPI()
		if spec.Components == nil || spec.Components.SecuritySchemes == nil {
			t.Fatal("a sub-API document declares no securitySchemes")
		}
		scheme, ok := spec.Components.SecuritySchemes[bearerSchemeName]
		if !ok {
			t.Fatalf("securitySchemes has no %q entry", bearerSchemeName)
		}
		if scheme.Type != "http" || scheme.Scheme != "bearer" {
			t.Errorf("scheme is %s/%s, want http/bearer", scheme.Type, scheme.Scheme)
		}
	}
}

// TestSpecSecurityMatchesTheMiddleware asserts the document says what
// the router does: every operation the auth middleware guards declares
// the bearer requirement, and no operation on the public surface does.
//
// The two halves are established independently — the operations that
// answer 401 without credentials are pinned by
// TestAuthenticatedSubRouterAlwaysAuthenticated, and the ones that never
// do by TestPublicSubRouterIsAuthFree — so this compares the contract
// against enforced behaviour rather than against another declaration.
func TestSpecSecurityMatchesTheMiddleware(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	security := opSecurity(t, res.APIs)

	if len(res.AuthenticatedOps) == 0 || len(res.PublicOps) == 0 {
		t.Fatal("expected both authenticated and public operations")
	}

	for _, op := range res.AuthenticatedOps {
		key := op.Method + " " + op.Path
		req := security[key]
		if len(req) == 0 {
			t.Errorf("%s (%s) rejects unauthenticated calls but the spec declares no security requirement", key, op.OperationID)
			continue
		}
		if _, ok := req[0][bearerSchemeName]; !ok {
			t.Errorf("%s (%s) declares %v, want the %q requirement", key, op.OperationID, req[0], bearerSchemeName)
		}
	}

	for _, op := range res.PublicOps {
		key := op.Method + " " + op.Path
		if req := security[key]; len(req) > 0 {
			t.Errorf("%s (%s) needs no credentials but the spec declares %v", key, op.OperationID, req)
		}
	}
}

// TestPublicRendersAreBudgetedPerShare covers what the per-address
// budget could not: a public link handed to a group of people who share
// one egress address.
func TestPublicRendersAreBudgetedPerShare(t *testing.T) {
	t.Parallel()
	deps := stubDeps(t)
	deps.DisableRateLimit = false
	res := BuildResult(deps)

	get := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/share/cal/"+token, nil)
		// One address for every caller, as a corporate NAT presents.
		req.RemoteAddr = "203.0.113.9:12345"
		rr := httptest.NewRecorder()
		res.Handler.ServeHTTP(rr, req)
		return rr.Code
	}

	// Spend one share's whole budget.
	for i := 0; i < publicRenderMaxRequests; i++ {
		if code := get("share-one"); code == http.StatusTooManyRequests {
			t.Fatalf("request %d for the first share was rejected before its budget ran out", i+1)
		}
	}
	if code := get("share-one"); code != http.StatusTooManyRequests {
		t.Fatalf("first share past its budget returned %d, want 429", code)
	}

	// A colleague at the same address opening a different share is not
	// paying for that. Under the previous per-address budget they were.
	if code := get("share-two"); code == http.StatusTooManyRequests {
		t.Error("a second share from the same address was rejected on the first request")
	}
}

// TestPublicInviteAcceptKeepsATightBudget pins the other half of the
// split: redeeming an invite is a write performed once, and guessing at
// it must stay expensive even though renders got room to breathe.
func TestPublicInviteAcceptKeepsATightBudget(t *testing.T) {
	t.Parallel()
	deps := stubDeps(t)
	deps.DisableRateLimit = false
	res := BuildResult(deps)

	post := func() int {
		req := httptest.NewRequest(http.MethodPost, "/public/invites/accept", nil)
		req.RemoteAddr = "203.0.113.10:12345"
		rr := httptest.NewRecorder()
		res.Handler.ServeHTTP(rr, req)
		return rr.Code
	}

	for i := 0; i < publicAcceptMaxRequests; i++ {
		if code := post(); code == http.StatusTooManyRequests {
			t.Fatalf("request %d was rejected before the budget ran out", i+1)
		}
	}
	if code := post(); code != http.StatusTooManyRequests {
		t.Errorf("past the budget the endpoint returned %d, want 429", code)
	}
}

// TestDisableRateLimitReachesThePublicSurface pins the escape hatch.
// The flag is what integration tests and single-tenant deployments use
// to stop many callers behind one loopback address from throttling each
// other, and the public limiter used to ignore it entirely.
func TestDisableRateLimitReachesThePublicSurface(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t)) // DisableRateLimit: true

	for i := 0; i < publicRenderMaxRequests*2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/share/cal/one-token", nil)
		req.RemoteAddr = "203.0.113.11:12345"
		rr := httptest.NewRecorder()
		res.Handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was rate limited with DisableRateLimit set", i+1)
		}
	}

	for i := 0; i < publicAcceptMaxRequests*2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/public/invites/accept", nil)
		req.RemoteAddr = "203.0.113.11:12345"
		rr := httptest.NewRecorder()
		res.Handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			t.Fatalf("accept request %d was rate limited with DisableRateLimit set", i+1)
		}
	}
}

func TestPathTail(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/share/cal/abc":      "abc",
		"/share/cal/abc/":     "abc",
		"/public/lenses/xyz":  "xyz",
		"/share/cal/":         "cal",
		"/health":             "health",
		"":                    "",
		"/":                   "",
		"/a/b/c/d/e/f/g/last": "last",
	}
	for in, want := range cases {
		if got := pathTail(in); got != want {
			t.Errorf("pathTail(%q) = %q, want %q", in, got, want)
		}
	}
}
