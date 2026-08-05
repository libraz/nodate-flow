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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	_ "github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	calendarq "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
)

// stubDeps returns the minimal Deps the router needs to build with
// every sub-API registered. The DB is a real *sql.DB pointing at an
// unreachable address: handlers that reach the database fail with a
// generic 500 instead of panicking on a nil Queries dereference, which
// keeps the static check focused on whether the auth middleware ran
// rather than whether each handler short-circuits on validation.
//
// CalendarQueries must be non-nil because the public-share render
// handler dereferences it before any auth-gate logic runs; leaving it
// nil panics the test before the assertion gets to execute.
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
		CalendarQueries:  calendarq.New(db),
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
	"actorId":       "01HX0000000000000000000000",
	"agentId":       "01HX0000000000000000000001",
	"aid":           "01HX0000000000000000000002",
	"attId":         "01HX000000000000000000000Q",
	"attendeeId":    "01HX000000000000000000000R",
	"cId":           "01HX000000000000000000000S",
	"cid":           "01HX0000000000000000000003",
	"calId":         "01HX000000000000000000000T",
	"depId":         "01HX0000000000000000000004",
	"eventPublicId": "01HX000000000000000000000Y",
	"evtId":         "01HX0000000000000000000005",
	"id":            "01HX0000000000000000000006",
	"importId":      "01HX0000000000000000000007",
	"inboxItemId":   "01HX0000000000000000000008",
	"inviteId":      "01HX000000000000000000000U",
	"itemId":        "01HX000000000000000000000V",
	"labelId":       "01HX0000000000000000000009",
	"lensId":        "01HX000000000000000000000A",
	"linkId":        "01HX000000000000000000000B",
	"memoId":        "01HX000000000000000000000W",
	"notifId":       "01HX000000000000000000000C",
	"pageId":        "01HX000000000000000000000D",
	"prjId":         "01HX000000000000000000000E",
	"providerId":    "01HX000000000000000000000F",
	"reactionId":    "01HX000000000000000000000G",
	"shareId":       "01HX000000000000000000000X",
	"snowflake":     "123456789012345678",
	"suggestionId":  "01HX000000000000000000000H",
	"taskId":        "01HX000000000000000000000I",
	"timeboxId":     "01HX000000000000000000000J",
	"token":         "share-token-stub",
	"tokenId":       "01HX000000000000000000000K",
	"userId":        "01HX000000000000000000000L",
	"versionId":     "01HX000000000000000000000M",
	"webhookId":     "01HX000000000000000000000N",
	"widgetId":      "01HX000000000000000000000O",
	"wsId":          "01HX000000000000000000000P",
}

// resolvePath substitutes Huma {param} placeholders with fixture values
// suitable for chi's pattern matcher. Unknown parameters fail the test
// loud so a new route does not silently bypass the static check.
func resolvePath(t *testing.T, p string) string {
	t.Helper()
	out := p
	for {
		openIdx := strings.Index(out, "{")
		if openIdx < 0 {
			break
		}
		closeIdx := strings.Index(out[openIdx:], "}")
		if closeIdx < 0 {
			t.Fatalf("malformed path template: %q", p)
		}
		name := out[openIdx+1 : openIdx+closeIdx]
		val, ok := pathPlaceholders[name]
		if !ok {
			t.Fatalf("path %q references unknown parameter %q — add it to pathPlaceholders", p, name)
		}
		out = out[:openIdx] + val + out[openIdx+closeIdx+1:]
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

// TestEveryOperationHasDescription walks every huma.Operation registered
// against any sub-API and fails if any one has an empty Description.
//
// Description carries auth requirements, idempotency notes, and side
// effects that consumers (SDK readers, future LLM tooling, partner
// integrations) need to use the endpoint correctly. Summary alone is
// not enough — it is a four-word headline. We require a real
// 1-2 sentence Description on every operation.
//
// When this test fails it lists every operation that is missing the
// field. Add `Description: "..."` to the offending huma.Operation
// (typically in apps/flow-api/internal/http/router/router.go or in a
// handlers/<feature>/register.go file) and re-run.
func TestEveryOperationHasDescription(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))

	type missing struct {
		method      string
		path        string
		operationID string
		summary     string
	}
	seen := map[string]missing{}

	for _, a := range res.APIs {
		spec := a.OpenAPI()
		if spec == nil || spec.Paths == nil {
			continue
		}
		for path, item := range spec.Paths {
			if item == nil {
				continue
			}
			verbs := map[string]*huma.Operation{
				http.MethodGet:     item.Get,
				http.MethodPost:    item.Post,
				http.MethodPut:     item.Put,
				http.MethodPatch:   item.Patch,
				http.MethodDelete:  item.Delete,
				http.MethodHead:    item.Head,
				http.MethodOptions: item.Options,
			}
			for method, op := range verbs {
				if op == nil {
					continue
				}
				if strings.TrimSpace(op.Description) == "" {
					key := method + " " + path + " " + op.OperationID
					seen[key] = missing{
						method:      method,
						path:        path,
						operationID: op.OperationID,
						summary:     op.Summary,
					}
				}
			}
		}
	}
	bad := make([]missing, 0, len(seen))
	for _, m := range seen {
		bad = append(bad, m)
	}

	if len(bad) > 0 {
		// Stable ordering so the failure is diff-friendly across runs.
		sort.Slice(bad, func(i, j int) bool {
			if bad[i].path != bad[j].path {
				return bad[i].path < bad[j].path
			}
			return bad[i].method < bad[j].method
		})
		t.Errorf("%d operations are missing huma.Operation.Description:", len(bad))
		for _, m := range bad {
			t.Errorf("  %-6s %s (%s) — summary: %q", m.method, m.path, m.operationID, m.summary)
		}
	}
}

// TestEveryOperationHasTags asserts that every huma.Operation declares
// at least one Tag. Tags drive OpenAPI navigation grouping; missing
// tags collapse the SDK side-bar into a single un-grouped list. Same
// failure surface as TestEveryOperationHasDescription — the failure
// message lists each offender so the fix is mechanical.
func TestEveryOperationHasTags(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))

	type missing struct {
		method      string
		path        string
		operationID string
		summary     string
	}
	seen := map[string]missing{}

	for _, a := range res.APIs {
		spec := a.OpenAPI()
		if spec == nil || spec.Paths == nil {
			continue
		}
		for path, item := range spec.Paths {
			if item == nil {
				continue
			}
			verbs := map[string]*huma.Operation{
				http.MethodGet:     item.Get,
				http.MethodPost:    item.Post,
				http.MethodPut:     item.Put,
				http.MethodPatch:   item.Patch,
				http.MethodDelete:  item.Delete,
				http.MethodHead:    item.Head,
				http.MethodOptions: item.Options,
			}
			for method, op := range verbs {
				if op == nil {
					continue
				}
				if len(op.Tags) == 0 {
					key := method + " " + path + " " + op.OperationID
					seen[key] = missing{
						method:      method,
						path:        path,
						operationID: op.OperationID,
						summary:     op.Summary,
					}
				}
			}
		}
	}

	bad := make([]missing, 0, len(seen))
	for _, m := range seen {
		bad = append(bad, m)
	}

	if len(bad) > 0 {
		sort.Slice(bad, func(i, j int) bool {
			if bad[i].path != bad[j].path {
				return bad[i].path < bad[j].path
			}
			return bad[i].method < bad[j].method
		})
		t.Errorf("%d operations are missing huma.Operation.Tags:", len(bad))
		for _, m := range bad {
			t.Errorf("  %-6s %s (%s) — summary: %q", m.method, m.path, m.operationID, m.summary)
		}
	}
}

func TestP3SDKAdvertisedEndpointsHaveHandlers(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	want := map[string]struct{}{
		"GET /tasks/{id}/agents":                                      {},
		"POST /tasks/{id}/agents":                                     {},
		"GET /workspaces/{wsId}/calendar-events/{evtId}/linked-tasks": {},
		"GET /workspaces/{wsId}/relation-suggestions":                 {},
		"GET /tasks/{id}/relation-suggestions":                        {},
		"POST /relation-suggestions/{suggestionId}/resolve":           {},
	}
	seen := map[string]struct{}{}
	for _, op := range res.AuthenticatedOps {
		seen[op.Method+" "+op.Path] = struct{}{}
	}
	for endpoint := range want {
		if _, ok := seen[endpoint]; !ok {
			t.Errorf("SDK-advertised endpoint %s is missing from authenticated router registration", endpoint)
		}
	}
}
