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
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// isSafeMethod reports whether the HTTP method is read-only. Role floors and
// the scope checks only apply to the mutating half.
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

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

// roleFloorExemptOps lists every mutating operation on the authenticated
// surface that deliberately runs without a group-level workspace/project role
// floor, together with the reason it is safe.
//
// The map is the whole exemption budget: TestEveryMutatingOpHasARoleFloor
// fails both when an unlisted mutation has no floor (a new route slipped in
// without a role decision) and when a listed one turns out to have one or to
// no longer exist (a stale entry). Grant an exemption only when the operation
// enforces its authorization somewhere the router cannot see, and say where.
//
// Deciding whether an operation needs a workspace role floor: ask what rows
// the request writes. The workspace role floor exists to protect state the
// workspace shares — labels, timeboxes, lenses, pages, dashboards, imports,
// the intake queue — where one member's edit changes what every other member
// sees. It is NOT a general "guests may not POST" rule: an operation whose
// every write is bound to the caller (user_id = actor: their notifications,
// their favorites, their inbox rows, their own tokens) has no shared state to
// protect, and putting a floor on it only removes the caller's control over
// their own account.
// #nosec G101 -- operation ids mapped to prose reasons; no credentials here
var roleFloorExemptOps = map[string]string{
	// Resolved through RequireProjectMemberByGlobalID; the handlers apply
	// the project-role rules themselves because the required role differs
	// per operation (lead for membership changes, editor for metadata).
	"projects-patch":          "project role checked in handler",
	"projects-disable":        "project role checked in handler",
	"projects-members-add":    "project role checked in handler",
	"projects-members-remove": "project role checked in handler",

	// No workspace path parameter to hang a floor on: the project comes
	// from the request body and the handlers gate on project editor via
	// tasks.requireProjectEditor.
	"tasks-create":  "project editor checked in handler",
	"tasks-reorder": "project editor checked in handler",

	// Accepts either the internal service token or a workspace member
	// bearer; membership is resolved from the body through
	// resolve.WorkspaceMember.
	"signals-create": "workspace membership checked in handler",

	// Caller-scoped rows. Ownership, not workspace role, is the access rule:
	// every statement is bound by user_id = actor, so the request cannot
	// change what any other member sees.
	"inbox-archive":                   "caller-scoped row",
	"inbox-snooze":                    "caller-scoped row",
	"notifications-mark-read":         "caller-scoped row",
	"notifications-archive":           "caller-scoped row",
	"notifications-mark-all-read":     "caller-scoped row",
	"notification-preferences-update": "caller-scoped row",
	"favorites-create":                "caller-scoped row",
	"favorites-delete":                "caller-scoped row",
	"mcp-tokens-create":               "caller-scoped row",
	"mcp-tokens-delete":               "caller-scoped row",
	"relation-suggestions-resolve":    "workspace membership checked in handler",

	// Calendar surface. The write floor is enforced in the handlers rather
	// than by a router-level floor: resolveCalendarWrite requires calendar
	// editor or above (and refuses writes to system calendars), with
	// resolveWorkspaceNonGuest / resolveWorkspaceAdmin covering the
	// public-share admin routes.
	"calendars-create":                  "calendar ACL in handler",
	"calendars-subscribe-system":        "calendar ACL in handler",
	"calendars-self-subscribe":          "calendar ACL in handler",
	"calendars-patch":                   "calendar ACL in handler",
	"calendars-delete":                  "calendar ACL in handler",
	"calendars-self-subscription-patch": "calendar ACL in handler",
	"public-shares-create":              "calendar ACL in handler",
	"public-shares-patch":               "calendar ACL in handler",
	"public-shares-rotate":              "calendar ACL in handler",
	"public-shares-delete":              "calendar ACL in handler",
	"public-shares-events-attach":       "calendar ACL in handler",
	"public-shares-events-detach":       "calendar ACL in handler",
	"public-shares-events-reorder":      "calendar ACL in handler",
	"events-create":                     "calendar ACL in handler",
	"events-patch":                      "calendar ACL in handler",
	"events-delete":                     "calendar ACL in handler",
	"events-smart-create":               "calendar ACL in handler",
	"events-from-task":                  "calendar ACL in handler",
	"members-add":                       "calendar ACL in handler",
	"members-update-role":               "calendar ACL in handler",
	"members-remove":                    "calendar ACL in handler",
	"attendees-add":                     "calendar ACL in handler",
	"attendees-remove":                  "calendar ACL in handler",
	"attendees-rsvp":                    "calendar ACL in handler",
	"attendees-can-edit":                "calendar ACL in handler",
	"event-invites-create":              "calendar ACL in handler",
	"event-invites-revoke":              "calendar ACL in handler",
	"comments-create":                   "calendar ACL in handler",
	"comments-edit":                     "calendar ACL in handler",
	"comments-delete":                   "calendar ACL in handler",
	"checklist-create":                  "calendar ACL in handler",
	"checklist-update":                  "calendar ACL in handler",
	"checklist-delete":                  "calendar ACL in handler",
	"memos-create":                      "calendar ACL in handler",
	"memos-update":                      "calendar ACL in handler",
	"memos-delete":                      "calendar ACL in handler",
	"attachments-presign":               "calendar ACL in handler",
	"attachments-confirm":               "calendar ACL in handler",
	"attachments-delete":                "calendar ACL in handler",
}

// TestEveryMutatingOpHasARoleFloor asserts that every mutating operation on
// the authenticated surface either sits behind a chi group that mounts a role
// floor, or is named in roleFloorExemptOps.
//
// The floor recorded on each operation comes from mountGroup, which mounts
// the middleware and records the label in the same call, so this cannot pass
// on a group that merely claims a floor.
//
// Without such a check, "guest" — documented as the read-only workspace role
// — was a full write role across labels, lenses, pages, timeboxes, imports
// and the AI surface: a group can gain a mutating route long after its
// middleware stack was decided, and nothing said so.
func TestEveryMutatingOpHasARoleFloor(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))

	used := map[string]bool{}
	var offenders []OperationRef
	for _, op := range res.AuthenticatedOps {
		if isSafeMethod(op.Method) {
			continue
		}
		if op.WriteFloor != floorNone.label {
			if _, exempt := roleFloorExemptOps[op.OperationID]; exempt {
				t.Errorf("%s is listed in roleFloorExemptOps but its group now mounts the %q floor — drop the exemption",
					op.OperationID, op.WriteFloor)
				used[op.OperationID] = true
			}
			continue
		}
		if _, exempt := roleFloorExemptOps[op.OperationID]; exempt {
			used[op.OperationID] = true
			continue
		}
		offenders = append(offenders, op)
	}

	sort.Slice(offenders, func(i, j int) bool {
		if offenders[i].Path != offenders[j].Path {
			return offenders[i].Path < offenders[j].Path
		}
		return offenders[i].Method < offenders[j].Method
	})
	for _, op := range offenders {
		t.Errorf("mutating op %-6s %s (%s) has no workspace/project role floor — mount one via mountGroup, or add it to roleFloorExemptOps with the reason it is safe",
			op.Method, op.Path, op.OperationID)
	}

	for id := range roleFloorExemptOps {
		if !used[id] {
			t.Errorf("roleFloorExemptOps lists %q, which is no longer a floorless mutating operation — remove the stale entry", id)
		}
	}
}

// tokenBindingCrossWorkspaceOps lists the authenticated operations that
// deliberately span every workspace the caller belongs to and therefore
// refuse a workspace-bound PAT / MCP token outright.
//
// Everything else must be reachable for a bound token only after the binding
// has been compared against a concrete workspace — either from the {wsId}
// path parameter (RequireTokenWorkspaceBinding) or from the workspace the
// handler resolves (acl.CheckWorkspaceMember).
var tokenBindingCrossWorkspaceOps = map[string]struct{}{
	"me-tasks-list":                {},
	"me-tasks-with-dates-list":     {},
	"me-calendar-events-list":      {},
	"me-invites-list":              {},
	"notifications-list":           {},
	"notifications-unread-count":   {},
	"notifications-mark-read":      {},
	"notifications-archive":        {},
	"favorites-list":               {},
	"favorites-create":             {},
	"favorites-delete":             {},
	"inbox-list":                   {},
	"inbox-archive":                {},
	"inbox-snooze":                 {},
	"relation-suggestions-resolve": {},
}

// TestBearerTokenWorkspaceBindingCoversEveryOp asserts that every operation on
// the authenticated surface is classified for the PAT / MCP workspace binding,
// and that the set of routes which reject bound tokens outright is exactly the
// declared cross-workspace set.
//
// Coverage of the check itself is inherited rather than re-derived here:
// RequireTokenWorkspaceBinding is part of the authMW chain, and
// TestAuthenticatedSubRouterAlwaysAuthenticated already proves that chain runs
// on every operation this builder registers. What this test pins down is the
// classification — which is where a new route can quietly acquire an
// unchecked path.
func TestBearerTokenWorkspaceBindingCoversEveryOp(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))

	used := map[string]struct{}{}
	for _, op := range res.AuthenticatedOps {
		scope := middleware.TokenWorkspaceScopeFor(op.Path)
		_, declared := tokenBindingCrossWorkspaceOps[op.OperationID]
		if scope == middleware.TokenWorkspaceScopeCrossWorkspace {
			if !declared {
				t.Errorf("op %-6s %s (%s) refuses workspace-bound tokens but is not declared cross-workspace — give the route a {wsId}, add its prefix to middleware.tokenWorkspaceDerivedRoutes once the handler resolves a workspace, or declare it in tokenBindingCrossWorkspaceOps",
					op.Method, op.Path, op.OperationID)
				continue
			}
			used[op.OperationID] = struct{}{}
			continue
		}
		if declared {
			t.Errorf("op %s is declared cross-workspace but its path now resolves a workspace — remove the stale entry", op.OperationID)
		}
	}

	for id := range tokenBindingCrossWorkspaceOps {
		if _, seen := used[id]; !seen {
			t.Errorf("tokenBindingCrossWorkspaceOps lists %q, which is not a registered cross-workspace operation — remove the stale entry", id)
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
