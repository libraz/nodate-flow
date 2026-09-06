// Package router route-tree checks.
//
// The rest of the router's static checks walk an inventory of operations.
// An inventory is built by the code being checked, and this one had a
// hole: huma leaves an operation marked Hidden out of the OpenAPI
// document, the inventory was read back off that document, and so every
// check built on it skipped the routes that were deliberately kept out of
// the published contract — which is exactly the set a reachability check
// most wants to see.
//
// The checks here start from the other end. chi's route tree is what
// answers requests, so a path reaches it by existing rather than by being
// recorded, and walking it asks "what can arrive" instead of "what did we
// write down". Three things follow:
//
//   - every path in the tree is either a registered operation or a
//     declared raw route, so nothing is mounted that no check knows about;
//   - every path in the tree either belongs to the public surface or
//     refuses an anonymous caller;
//   - the /internal/* group is mounted behind the service-token guard that
//     admits no user bearer, and behind that one specifically.
package router

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// internalRoutePrefix is the path prefix reserved for endpoints that only
// other backend processes may call. Nothing under it is reachable with a
// user session, which is a property of the group's middleware rather than
// of the prefix, so the prefix is what the checks below use to find the
// routes whose middleware has to be verified.
const internalRoutePrefix = "/internal/"

// The two service-token middlewares, named as the exemption checks name
// functions: as package-qualified symbols the source walk can resolve, so
// a rename fails here instead of silently matching nothing.
// #nosec G101 -- Go symbol names the source walk resolves; no credential
const (
	serviceTokenOnlyMiddleware = "middleware.RequireServiceTokenOnly"
	signalsAuthMiddleware      = "middleware.RequireSignalsAuth"
)

// mcpRoutePattern is the one route whose guard runs behind a transport
// check. A bare request to it is refused for the wrong reason — malformed
// frame, not missing credential — so probing it without a well-formed
// frame would prove nothing about whether it authenticates.
const mcpRoutePattern = "/mcp"

// mcpInitializeFrame is the smallest well-formed JSON-RPC frame the MCP
// transport accepts, so the response to it is decided by the bearer.
const mcpInitializeFrame = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"router-route-tree","version":"0"}}}`

// unregisteredRoute describes a path in the chi route tree that no huma
// operation put there: a raw chi handler, or one of the routes huma's own
// config mounts for the document and the reference UI.
//
// Every such route has to be declared, because the operation inventory
// cannot see it — it is not an operation. The declaration also states what
// an unauthenticated caller gets, so a route that starts answering
// something else fails rather than blending into a surface nothing walks.
type unregisteredRoute struct {
	// refusesAnonymous is true when a request carrying no credentials is
	// answered with 401. False means the route answers anonymous callers
	// deliberately, and the reason has to say why that is safe.
	refusesAnonymous bool
	reason           string
}

// unregisteredRoutes is the whole budget of routable paths that no huma
// operation registered. A path in the tree that is neither a registered
// operation nor listed here fails TestRoutableTreeMatchesRegisteredOperations.
var unregisteredRoutes = map[string]unregisteredRoute{
	// Realtime and webhook handlers: raw chi routes because their response
	// shapes (a long-lived event stream, a payload dictated by an external
	// service) do not fit the Huma request/response model.
	"GET /workspaces/{wsId}/stream": {
		refusesAnonymous: true,
		reason:           "the SSE stream is mounted inside the workspace-member group, so it runs the same bearer check as the operations around it",
	},
	"POST /webhooks/github": {
		refusesAnonymous: true,
		reason:           "the receiver verifies its own HMAC signature and refuses an unsigned delivery",
	},
	"POST /webhooks/slack": {
		refusesAnonymous: true,
		reason:           "the receiver verifies its own signing secret and refuses an unsigned delivery",
	},
	"POST /webhooks/google": {
		refusesAnonymous: true,
		reason:           "the receiver compares the configured channel token and refuses a delivery without it",
	},

	// MCP transport. The handler owns its own auth because the bearer is an
	// mcp_ token rather than a session, so it is registered with chi's
	// all-method Handle and answers every verb.
	"GET " + mcpRoutePattern: {
		refusesAnonymous: true,
		reason:           "the transport refuses a stream request carrying no mcp bearer",
	},
	"POST " + mcpRoutePattern: {
		refusesAnonymous: true,
		reason:           "the transport refuses a call carrying no mcp bearer",
	},
	"CONNECT " + mcpRoutePattern: mcpMethodNotAllowed,
	"DELETE " + mcpRoutePattern:  mcpMethodNotAllowed,
	"HEAD " + mcpRoutePattern:    mcpMethodNotAllowed,
	"OPTIONS " + mcpRoutePattern: mcpMethodNotAllowed,
	"PATCH " + mcpRoutePattern:   mcpMethodNotAllowed,
	"PUT " + mcpRoutePattern:     mcpMethodNotAllowed,
	"QUERY " + mcpRoutePattern:   mcpMethodNotAllowed,
	"TRACE " + mcpRoutePattern:   mcpMethodNotAllowed,

	// Specification and reference-UI routes, all three mounted by
	// BuildResult after every builder has run. They serve the published
	// contract, which is public by construction: an SDK generator and the
	// reference UI both read it before anyone has a token.
	//
	// The set is deliberately this small. huma's config mounts a document
	// route per extension and a docs UI of its own, and flow-api builds one
	// API per chi group, so anything left to the config lands on this mux
	// once per group and is answered by whichever group was constructed
	// last. See TestConfigDerivedSpecRoutesAreNotMounted.
	"GET /openapi.json": specRoute,
	"GET /schemas/{schema}": {
		refusesAnonymous: false,
		reason:           "serves the JSON Schema of a request or response body, which is the address every response's own $schema field points at and is part of the same published contract as the document that references it",
	},
	"GET /api-reference": {
		refusesAnonymous: false,
		reason:           "static reference-UI page; it renders the published document and reads nothing else",
	},
}

// mcpMethodNotAllowed is the shared declaration for the verbs chi routes
// to the MCP handler because it was mounted for every method. The
// transport rejects them before any auth question is reached, so they are
// not 401 and not a way in either.
var mcpMethodNotAllowed = unregisteredRoute{
	refusesAnonymous: false,
	reason:           "the MCP transport allows only GET and POST and rejects the verb outright, so the request never reaches anything that could answer it",
}

// specRoute is the shared declaration for the routes that serve the
// OpenAPI document itself.
var specRoute = unregisteredRoute{
	refusesAnonymous: false,
	reason:           "serves the published OpenAPI document, which describes the API rather than any workspace's data",
}

// routeKey is the "METHOD /path" spelling both tables and both walks use.
func routeKey(method, path string) string {
	return method + " " + path
}

// registeredOperations indexes every operation the router recorded,
// across all three builders, by route key.
func registeredOperations(res Result) map[string]OperationRef {
	out := map[string]OperationRef{}
	for _, set := range [][]OperationRef{res.AuthenticatedOps, res.PublicOps, res.AuthOps} {
		for _, op := range set {
			out[routeKey(op.Method, op.Path)] = op
		}
	}
	return out
}

// anonymousRequest builds the unauthenticated probe for a route.
func anonymousRequest(t *testing.T, method, pattern string) *http.Request {
	t.Helper()
	url := resolvePath(t, pattern)
	if pattern != mcpRoutePattern {
		return httptest.NewRequest(method, url, nil)
	}
	req := httptest.NewRequest(method, url, strings.NewReader(mcpInitializeFrame))
	req.Header.Set("Content-Type", "application/json")
	// The GET transport is the event stream, which is refused as malformed
	// before it is refused as unauthenticated unless it asks for one.
	req.Header.Set("Accept", "text/event-stream")
	return req
}

// TestRoutableTreeMatchesRegisteredOperations asserts that the router
// mounts nothing the operation inventory cannot see.
//
// The inventory is complete for anything registered through huma, hidden
// operations included, because it is taken by huma.Register itself. What
// it cannot cover is a handler mounted straight onto chi — and those are
// the routes the document-driven checks have never walked. Declaring each
// one here is what puts it in front of the check below.
func TestRoutableTreeMatchesRegisteredOperations(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	if len(res.RoutableOps) == 0 {
		t.Fatal("the chi route tree is empty; every check in this file would be looking at nothing")
	}

	registered := registeredOperations(res)
	inTree := map[string]bool{}
	var undeclared []string
	for _, rr := range res.RoutableOps {
		key := routeKey(rr.Method, rr.Path)
		inTree[key] = true
		if _, ok := registered[key]; ok {
			continue
		}
		if _, ok := unregisteredRoutes[key]; ok {
			continue
		}
		undeclared = append(undeclared, key)
	}
	sort.Strings(undeclared)
	for _, key := range undeclared {
		t.Errorf("%s is routable but no huma operation registered it and unregisteredRoutes does not declare it — register it through huma, or declare it there with what an anonymous caller gets and why", key)
	}

	stale := make([]string, 0, len(unregisteredRoutes))
	for key := range unregisteredRoutes {
		if !inTree[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("unregisteredRoutes declares %s, which the router no longer mounts — remove the stale entry", key)
	}
}

// TestEveryRoutablePathIsPublicOrRefusesAnonymous drives an
// unauthenticated request at every path the mux will match and asserts it
// is answered with 401 unless it belongs to the public surface.
//
// Walking the tree rather than an operation list is the point: a route
// that no builder recorded, or one recorded but hidden, is refused here on
// the strength of being routable at all. The public surface is the
// operations buildPublicShareAPI registers plus the raw routes declared
// above as answering anonymous callers; everything else has to refuse.
func TestEveryRoutablePathIsPublicOrRefusesAnonymous(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	if len(res.RoutableOps) == 0 {
		t.Fatal("the chi route tree is empty; this check would assert nothing")
	}

	public := map[string]bool{}
	for _, op := range res.PublicOps {
		public[routeKey(op.Method, op.Path)] = true
	}
	for key, decl := range unregisteredRoutes {
		if !decl.refusesAnonymous {
			public[key] = true
		}
	}

	for _, rr := range res.RoutableOps {
		key := routeKey(rr.Method, rr.Path)
		req := anonymousRequest(t, rr.Method, rr.Path)
		rec := httptest.NewRecorder()
		res.Handler.ServeHTTP(rec, req)

		if public[key] {
			if rec.Code == http.StatusUnauthorized {
				t.Errorf("%s is declared part of the public surface but refuses an anonymous caller with 401 — it is no longer public, so move it off the public builder or drop its unregisteredRoutes declaration", key)
			}
			continue
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s answered an anonymous request with %d, want 401 — a routable path is either public or refuses callers without credentials", key, rec.Code)
		}
	}
}

// serviceTokenOnlyOpIDs returns the operations registered on a chi group
// whose auth middleware is RequireServiceTokenOnly.
//
// No user bearer reaches these at all — not a JWT, not a PAT, not an MCP
// token — so the checks that classify what a user token may do with an
// operation have nothing to say about them.
func serviceTokenOnlyOpIDs(src *httpSource) map[string]bool {
	out := map[string]bool{}
	for _, g := range src.groups {
		if !g.middleware[serviceTokenOnlyMiddleware] {
			continue
		}
		for id := range g.ops {
			out[id] = true
		}
	}
	return out
}

// TestInternalRoutesMountServiceTokenOnly asserts that every /internal/*
// path is registered on a chi group guarded by RequireServiceTokenOnly,
// and by that middleware rather than by RequireSignalsAuth.
//
// The distinction is the whole check. RequireSignalsAuth compares the
// bearer against the service token and, on anything that does not match,
// hands the request to the JWT chain — so a group mounting it admits any
// valid user token. That is correct for /signals, which real users post
// to, and wrong for /internal/*, whose handlers run without an actor and
// were never meant to be reachable from a session. The two middlewares
// differ by one fallthrough and read almost identically at the call site,
// which is why the group's mount is asserted rather than assumed.
func TestInternalRoutesMountServiceTokenOnly(t *testing.T) {
	t.Parallel()

	src := parseHTTPSource(t)
	if len(src.groups) == 0 {
		t.Fatal("the source walk found no chi groups; the middleware assertions below would be checking nothing")
	}
	if !src.declares(serviceTokenOnlyMiddleware) {
		t.Fatalf("%s does not exist in internal/http — the assertions below would match no group", serviceTokenOnlyMiddleware)
	}
	if !src.declares(signalsAuthMiddleware) {
		t.Fatalf("%s does not exist in internal/http — the assertion that no internal group mounts it would be vacuous", signalsAuthMiddleware)
	}

	res := BuildResult(stubDeps(t))
	internalOps := map[string]OperationRef{}
	for key, op := range registeredOperations(res) {
		if strings.HasPrefix(op.Path, internalRoutePrefix) {
			internalOps[key] = op
		}
	}
	if len(internalOps) == 0 {
		t.Fatalf("no registered operation has a path under %q; this check has nothing to hold", internalRoutePrefix)
	}

	for _, key := range sortedKeys(internalOps) {
		op := internalOps[key]
		groups := src.groupsRegistering(op.OperationID)
		if len(groups) == 0 {
			t.Errorf("%s (%s) is routable under %q but no chi group in router.go was found registering it — its middleware cannot be established",
				key, op.OperationID, internalRoutePrefix)
			continue
		}
		for _, g := range groups {
			if !g.middleware[serviceTokenOnlyMiddleware] {
				t.Errorf("%s (%s) is registered by the group at %s, which does not mount %s — an internal endpoint must be closed to every user bearer",
					key, op.OperationID, g.pos, serviceTokenOnlyMiddleware)
			}
			if g.middleware[signalsAuthMiddleware] {
				t.Errorf("%s (%s) is registered by the group at %s, which mounts %s — that middleware falls through to the JWT chain on a bearer that does not match the service token, so any valid user token would reach an endpoint that runs without an actor",
					key, op.OperationID, g.pos, signalsAuthMiddleware)
			}
		}
	}

	// The tree is the reachability answer: an /internal path mounted
	// outside the guarded group would not appear among the operations above
	// at all, so asking the tree is what notices it.
	for _, rr := range res.RoutableOps {
		if !strings.HasPrefix(rr.Path, internalRoutePrefix) {
			continue
		}
		if _, ok := internalOps[routeKey(rr.Method, rr.Path)]; !ok {
			t.Errorf("%s is routable under %q but is not a registered operation, so no chi group can be shown to guard it — register it through huma on the service-token group",
				routeKey(rr.Method, rr.Path), internalRoutePrefix)
		}
	}

	// The guard is scoped the other way too: a group closed to every user
	// bearer must not be carrying a route users are supposed to call.
	for id := range serviceTokenOnlyOpIDs(src) {
		found := false
		for _, op := range internalOps {
			if op.OperationID == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s is registered on a group mounting %s but its path is not under %q — either it belongs on the internal prefix or it belongs on a group users can reach",
				id, serviceTokenOnlyMiddleware, internalRoutePrefix)
		}
	}
}

// sortedKeys returns the map's keys in a stable order so failures are
// diff-friendly across runs.
func sortedKeys(m map[string]OperationRef) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// internalRoutesInTree returns every routable /internal/* path.
func internalRoutesInTree(t *testing.T, res Result) []RoutableRef {
	t.Helper()
	var out []RoutableRef
	for _, rr := range res.RoutableOps {
		if strings.HasPrefix(rr.Path, internalRoutePrefix) {
			out = append(out, rr)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no routable path under %q; the service-token checks would assert nothing", internalRoutePrefix)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// TestInternalRoutesRefuseEveryBearerButTheServiceToken is what gives the
// 401s elsewhere in this file their meaning.
//
// stubDeps leaves FlowAPISignalToken empty, and RequireServiceTokenOnly
// answers 401 to everything when it is unset. So a 401 observed against
// that harness is consistent with two very different states: a guard
// comparing a credential and rejecting it, or a group closed because
// nothing was configured. The distinguishing evidence is a request that
// presents the configured token and is NOT refused — without it, deleting
// the comparison and returning 401 unconditionally would pass every other
// check here.
func TestInternalRoutesRefuseEveryBearerButTheServiceToken(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDepsWithServiceToken(t))

	for _, rr := range internalRoutesInTree(t, res) {
		key := routeKey(rr.Method, rr.Path)

		for _, probe := range []struct {
			name   string
			bearer string
		}{
			{name: "no credentials", bearer: ""},
			// #nosec G101 -- a value chosen to differ from the configured secret
			{name: "a bearer that is not the service token", bearer: "no-such-secret"},
			{name: "the service token with a trailing byte", bearer: serviceTokenForTests + "x"},
			{name: "a prefix of the service token", bearer: serviceTokenForTests[:len(serviceTokenForTests)-1]},
		} {
			req := anonymousRequest(t, rr.Method, rr.Path)
			if probe.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+probe.bearer)
			}
			rec := httptest.NewRecorder()
			res.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s with %s answered %d, want 401", key, probe.name, rec.Code)
			}
		}

		req := anonymousRequest(t, rr.Method, rr.Path)
		req.Header.Set("Authorization", "Bearer "+serviceTokenForTests)
		rec := httptest.NewRecorder()
		res.Handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s refused the configured service token with 401 — the guard is not comparing the credential, so every other 401 on this route says nothing about it", key)
		}
	}
}

// TestInternalRoutesAreClosedWithoutAConfiguredServiceToken pins the other
// half: with no token configured the group admits nobody, including a
// caller presenting what would be the right token elsewhere. Leaving the
// secret unset must close /internal/*, not open it.
func TestInternalRoutesAreClosedWithoutAConfiguredServiceToken(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t)) // FlowAPISignalToken is empty here.

	for _, rr := range internalRoutesInTree(t, res) {
		req := anonymousRequest(t, rr.Method, rr.Path)
		req.Header.Set("Authorization", "Bearer "+serviceTokenForTests)
		rec := httptest.NewRecorder()
		res.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s answered %d with no service token configured, want 401 — an unconfigured secret must close the group rather than open it",
				routeKey(rr.Method, rr.Path), rec.Code)
		}
	}
}

// TestServiceTokenIsScopedToItsOwnRouteGroups asserts the token opens the
// two groups it is for and nothing else. Without the positive half the
// negative one is empty — every route would "reject the service token" if
// the harness token were simply wrong.
func TestServiceTokenIsScopedToItsOwnRouteGroups(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDepsWithServiceToken(t))

	send := func(method, pattern string) int {
		req := anonymousRequest(t, method, pattern)
		req.Header.Set("Authorization", "Bearer "+serviceTokenForTests)
		rec := httptest.NewRecorder()
		res.Handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// /signals is the other group the token is for: it takes the service
	// token and falls through to the JWT chain for everyone else.
	if code := send(http.MethodPost, "/signals"); code == http.StatusUnauthorized {
		t.Error("POST /signals refused the configured service token; the token used by the checks in this file is not the one the router accepts")
	}

	// Everything else rejects it even though it is configured. A shared
	// secret that opened the ordinary API would be a second, roleless way
	// into every workspace.
	for _, elsewhere := range []struct {
		method  string
		pattern string
	}{
		{http.MethodGet, "/workspaces/{wsId}/projects"},
		{http.MethodGet, "/workspaces/{wsId}/labels"},
		{http.MethodGet, "/me/tasks"},
	} {
		if code := send(elsewhere.method, elsewhere.pattern); code != http.StatusUnauthorized {
			t.Errorf("%s answered %d for a caller presenting the service token, want 401 — the token is scoped to /signals and %s",
				routeKey(elsewhere.method, elsewhere.pattern), code, internalRoutePrefix)
		}
	}
}
