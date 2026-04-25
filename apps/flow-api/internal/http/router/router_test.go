// Package router static-check tests (R6 Phase 0 / ADR 0007). The
// router has been split into three builders (buildAuthenticatedAPI /
// buildPublicShareAPI / buildAuthAPI) and the tests in this file are
// the safety net that prevents auth-bypass regressions when later
// phases relocate handlers between sub-routers.
//
// Strategy: build the router with stub deps (no DB, no cipher), then
// walk each sub-API's OpenAPI document and exercise every (method,
// path) pair through httptest.
//
//   - buildAuthenticatedAPI ⇒ every operation MUST short-circuit to 401
//     when the request carries no Authorization header. We assert the
//     authn middleware is the first thing the handler hits, so the
//     check is independent of whether the handler would later succeed.
//   - buildPublicShareAPI ⇒ no operation MAY return 401. Public
//     handlers may return 200, 400, 404, 429, etc., but never 401: that
//     would mean the auth middleware leaked into the public surface.
package router

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

// stubDeps returns the minimal Deps the router needs to build with
// every sub-API registered. The DB is a real *sql.DB pointing at an
// unreachable address: handlers that reach the database fail with a
// generic 500 instead of panicking on a nil Queries dereference, which
// keeps the static check focused on whether the auth middleware ran
// rather than whether each handler short-circuits on validation.
func stubDeps(t *testing.T) Deps {
	t.Helper()
	issuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		t.Fatalf("jwt issuer: %v", err)
	}
	db, err := sql.Open("mysql", "stub:stub@tcp(127.0.0.1:1)/stub?timeout=1ms")
	if err != nil {
		t.Fatalf("stub db open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return Deps{
		DB:               db,
		Queries:          generated.New(db),
		JWT:              issuer,
		DisableRateLimit: true,
	}
}

// pathPlaceholders maps every Huma path parameter the router uses to a
// concrete fixture value. The actual values do not matter — the auth
// middleware never inspects them — but they MUST be present for chi to
// match the route. Adding a new {param} to a route requires adding it
// here.
var pathPlaceholders = map[string]string{
	"actorId":      "01HX0000000000000000000000",
	"agentId":      "01HX0000000000000000000001",
	"aid":          "01HX0000000000000000000002",
	"attId":        "01HX000000000000000000000Q",
	"attendeeId":   "01HX000000000000000000000R",
	"cId":          "01HX000000000000000000000S",
	"cid":          "01HX0000000000000000000003",
	"calId":        "01HX000000000000000000000T",
	"depId":        "01HX0000000000000000000004",
	"evtId":        "01HX0000000000000000000005",
	"id":           "01HX0000000000000000000006",
	"importId":     "01HX0000000000000000000007",
	"inboxItemId":  "01HX0000000000000000000008",
	"inviteId":     "01HX000000000000000000000U",
	"itemId":       "01HX000000000000000000000V",
	"labelId":      "01HX0000000000000000000009",
	"lensId":       "01HX000000000000000000000A",
	"linkId":       "01HX000000000000000000000B",
	"memoId":       "01HX000000000000000000000W",
	"notifId":      "01HX000000000000000000000C",
	"pageId":       "01HX000000000000000000000D",
	"prjId":        "01HX000000000000000000000E",
	"providerId":   "01HX000000000000000000000F",
	"reactionId":   "01HX000000000000000000000G",
	"shareId":      "01HX000000000000000000000X",
	"suggestionId": "01HX000000000000000000000H",
	"taskId":       "01HX000000000000000000000I",
	"timeboxId":    "01HX000000000000000000000J",
	"token":        "share-token-stub",
	"tokenId":      "01HX000000000000000000000K",
	"userId":       "01HX000000000000000000000L",
	"versionId":    "01HX000000000000000000000M",
	"webhookId":    "01HX000000000000000000000N",
	"widgetId":     "01HX000000000000000000000O",
	"wsId":         "01HX000000000000000000000P",
}

// resolvePath substitutes Huma {param} placeholders with fixture values
// suitable for chi's pattern matcher. Unknown parameters fail the test
// loud so a new route does not silently bypass the static check.
func resolvePath(t *testing.T, p string) string {
	t.Helper()
	out := p
	for {
		open := strings.Index(out, "{")
		if open < 0 {
			break
		}
		close := strings.Index(out[open:], "}")
		if close < 0 {
			t.Fatalf("malformed path template: %q", p)
		}
		name := out[open+1 : open+close]
		val, ok := pathPlaceholders[name]
		if !ok {
			t.Fatalf("path %q references unknown parameter %q — add it to pathPlaceholders", p, name)
		}
		out = out[:open] + val + out[open+close+1:]
	}
	return out
}

// TestPublicSubRouterIsAuthFree asserts that no operation registered
// through buildPublicShareAPI returns 401 when called without
// credentials. A 401 here would mean the auth middleware leaked into
// the public surface, defeating the sub-router split that ADR 0007
// relies on for blast-radius isolation.
func TestPublicSubRouterIsAuthFree(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	if len(res.PublicOps) == 0 {
		t.Fatal("buildPublicShareAPI registered no operations")
	}

	for _, op := range res.PublicOps {
		url := resolvePath(t, op.Path)
		req := httptest.NewRequest(op.Method, url, nil)
		rr := httptest.NewRecorder()
		res.Handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusUnauthorized {
			t.Errorf("public op %s %s (%s) returned 401 — auth middleware leaked into public sub-router", op.Method, op.Path, op.OperationID)
		}
	}
}

// TestAuthenticatedSubRouterAlwaysAuthenticated asserts that every
// operation registered through buildAuthenticatedAPI returns 401 when
// called without credentials. Any non-401 here means a route was
// registered without going through the authMW chain — the kind of
// regression we are using static checks to catch.
func TestAuthenticatedSubRouterAlwaysAuthenticated(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	if len(res.AuthenticatedOps) == 0 {
		t.Fatal("buildAuthenticatedAPI registered no operations")
	}

	for _, op := range res.AuthenticatedOps {
		url := resolvePath(t, op.Path)
		req := httptest.NewRequest(op.Method, url, nil)
		rr := httptest.NewRecorder()
		res.Handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("authenticated op %s %s (%s) returned %d, want 401 — route is missing auth middleware", op.Method, op.Path, op.OperationID, rr.Code)
		}
	}
}

// TestAuthAPIBuilderIsEmpty pins the current shape of buildAuthAPI: it
// is a placeholder for the auth/identity surface and must not register
// operations until a future migration explicitly wires them up. When
// that work lands the builder will register routes and this test
// should be replaced with the appropriate isolation check.
func TestAuthAPIBuilderIsEmpty(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	for _, op := range res.AuthOps {
		t.Errorf("buildAuthAPI registered %s %s (%s) but is documented as a placeholder; update the test if this surface is now intentional", op.Method, op.Path, op.OperationID)
	}
}
