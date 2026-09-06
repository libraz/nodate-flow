// Package router static-check tests (ADR 0007). The
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

// serviceTokenForTests is the shared secret the service-token harness
// configures. It is a fixed literal because the checks that use it need
// both halves of the comparison: a request carrying it, and requests
// carrying values that differ from it in length and in content.
// #nosec G101 -- in-process test value; the router is built with it rather than reading any environment
const serviceTokenForTests = "router-checks-service-token"

// stubDepsWithServiceToken is stubDeps with FlowAPISignalToken set.
//
// It exists alongside stubDeps rather than replacing it because the two
// harnesses answer different questions. stubDeps leaves the secret unset,
// which is what most checks want: /internal/* is closed, /signals accepts
// only user bearers, and the surface under test is the ordinary one.
// A 401 observed against it, though, is ambiguous — a guard that compared
// a credential and a group that was never configured produce the same
// response. This harness is what tells them apart.
func stubDepsWithServiceToken(t *testing.T) Deps {
	t.Helper()
	deps := stubDeps(t)
	deps.FlowAPISignalToken = serviceTokenForTests
	return deps
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
	// /schemas/{schema} takes a component name, not an id. The value names
	// a schema the merged document actually declares, so probing the route
	// exercises a lookup that can succeed rather than one that always
	// misses.
	"schema":       "HealthOutputBody",
	"shareId":      "01HX000000000000000000000X",
	"snowflake":    "123456789012345678",
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
// It reads the registration record rather than the OpenAPI document,
// which is what makes it cover the hidden operations too. Those are the
// ones whose consumers have no reference UI and no generated client to
// read: another backend process calling them has the prose or nothing.
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

	for _, set := range [][]OperationRef{res.AuthenticatedOps, res.PublicOps, res.AuthOps} {
		for _, op := range set {
			if strings.TrimSpace(op.Description) != "" {
				continue
			}
			key := op.Method + " " + op.Path + " " + op.OperationID
			seen[key] = missing{
				method:      op.Method,
				path:        op.Path,
				operationID: op.OperationID,
				summary:     op.Summary,
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
//
// Unlike that one, this reads the published document, and deliberately:
// a tag is a heading in a rendered document, so it means nothing on an
// operation the document does not contain. Requiring one on a hidden
// operation would be requiring a value nothing reads.
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
// floor, together with the check that authorises it instead.
//
// The map is the whole exemption budget. TestEveryMutatingOpHasARoleFloor
// fails both when an unlisted mutation has no floor (a new route slipped in
// without a role decision) and when a listed one turns out to have one or to
// no longer exist (a stale entry). TestRoleFloorExemptionsNameCodeThatRuns
// then resolves every entry against the source: an exemption naming a check
// the handler does not reach, middleware the group does not mount, or a
// statement that does not bind its rows to the caller fails there.
//
// That second half is the point of the aclExemption shape. A sentence saying
// an operation is checked elsewhere stays true-looking after the check is
// deleted; a named function that has to be reachable does not.
//
// Deciding whether an operation needs a workspace role floor: ask what rows
// the request writes. The workspace role floor exists to protect state the
// workspace shares — labels, timeboxes, lenses, pages, dashboards, imports,
// the intake queue — where one member's edit changes what every other member
// sees. It is NOT a general "guests may not POST" rule: an operation whose
// every write is bound to the caller (their notifications, their favorites,
// their own tokens) has no shared state to protect, and putting a floor on it
// only removes the caller's control over their own account.
// #nosec G101 -- operation ids mapped to the checks that authorise them; no credentials here
var roleFloorExemptOps = map[string]aclExemption{
	// No workspace path parameter to hang a floor on: the project comes
	// from the request body, so the project role is compared in the handler
	// once the body has been resolved.
	"tasks-create": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"tasks.requireProjectEditor"},
		note:   "project taken from the request body, so the role comparison happens after it resolves",
	},
	"tasks-reorder": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"tasks.requireProjectEditor"},
		note:   "project taken from the request body, so the role comparison happens after it resolves",
	},

	// Accepts either the internal service token or a workspace member
	// bearer, so the workspace comes from the body rather than the path.
	"signals-create": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"resolve.WorkspaceMember"},
		note:   "workspace taken from the request body; the service-token path resolves the workspace without an actor",
	},

	// The intake queue belongs to the workspace, not to the caller: both
	// statements match on workspace_id and public_id alone, so one member's
	// archive or snooze takes the item off every other member's list. That
	// is the shared state a workspace floor protects — but the route carries
	// no {wsId} to hang a group floor on, so the same floor is asked for in
	// the handler.
	"inbox-archive": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"resolve.WorkspaceMemberForWrite"},
		note:   "workspace taken from the query string; the item is workspace-shared state, so the write floor keeps guests out of it",
	},
	"inbox-snooze": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"resolve.WorkspaceMemberForWrite"},
		note:   "workspace taken from the query string; the item is workspace-shared state, so the write floor keeps guests out of it",
	},

	// The suggestion's workspace and the caller's membership of it are
	// resolved in one statement, so a suggestion in a workspace the caller
	// does not belong to reads as absent.
	"relation-suggestions-resolve": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"relations.resolveSuggestionWorkspaceForActor"},
		note:   "the route carries no workspace parameter; the suggestion supplies it and membership is joined onto the same lookup",
	},

	// Caller-scoped writes. Ownership, not workspace role, is the access
	// rule: each statement below binds the rows it changes to the caller,
	// whose identity comes from the session rather than the request.
	"notifications-mark-read": {
		via:    auth.EnforcedByActorScopedWrite,
		writes: []actorScopedWrite{{query: "MarkNotificationRead", column: "recipient_user_id"}},
		note:   "the caller's own notification",
	},
	"notifications-archive": {
		via:    auth.EnforcedByActorScopedWrite,
		writes: []actorScopedWrite{{query: "ArchiveNotification", column: "recipient_user_id"}},
		note:   "the caller's own notification",
	},
	"notifications-mark-all-read": {
		via:    auth.EnforcedByActorScopedWrite,
		writes: []actorScopedWrite{{query: "MarkAllNotificationsRead", column: "recipient_user_id"}},
		note:   "the caller's own notifications within one workspace",
	},
	"notification-preferences-update": {
		via:    auth.EnforcedByActorScopedWrite,
		writes: []actorScopedWrite{{query: "UpsertNotificationPreference", column: "user_id"}},
		note:   "the caller's own delivery preferences",
	},
	"favorites-create": {
		via:    auth.EnforcedByActorScopedWrite,
		writes: []actorScopedWrite{{query: "CreateFavorite", column: "user_id"}},
		note:   "the caller's own pinned list",
	},
	"favorites-delete": {
		via:    auth.EnforcedByActorScopedWrite,
		writes: []actorScopedWrite{{query: "DisableFavorite", column: "user_id"}},
		note:   "the caller's own pinned list",
	},
	"mcp-tokens-create": {
		via:    auth.EnforcedByActorScopedWrite,
		writes: []actorScopedWrite{{query: "CreateMcpToken", column: "user_id"}},
		note:   "the caller's own token, which in turn carries no more than the caller's own role",
	},
	"mcp-tokens-delete": {
		via:    auth.EnforcedByActorScopedWrite,
		writes: []actorScopedWrite{{query: "RevokeMcpToken", column: "user_id"}},
		note:   "the caller's own token",
	},

	// Workspace-scoped calendar surface. These routes sit behind the bearer
	// check only, so the handler resolves the workspace and the role it
	// needs from {wsId} itself.
	"calendars-create": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveWorkspaceNonGuest"},
		note:   "a calendar is workspace state, not the creator's own row: it is discoverable to the workspace and the creator lands in it as owner, so the read-only role is refused",
	},
	"calendars-self-subscribe": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveWorkspace"},
		note:   "workspace membership; the only row written is the caller's own viewer membership of a calendar the workspace can already see, which is what a read-only role joining should get",
	},
	"public-shares-create": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveWorkspaceNonGuest"},
		note:   "publishing to a URL anyone may open is not a read-only role's decision",
	},
	"public-shares-patch": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveWorkspaceNonGuest"},
		note:   "publishing to a URL anyone may open is not a read-only role's decision",
	},
	"public-shares-rotate": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveWorkspaceNonGuest"},
		note:   "publishing to a URL anyone may open is not a read-only role's decision",
	},
	"public-shares-delete": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveWorkspaceAdmin"},
		note:   "withdrawing a published page is held to a higher role than publishing one",
	},
	"public-shares-events-attach": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveWorkspaceNonGuest"},
		note:   "publishing to a URL anyone may open is not a read-only role's decision",
	},
	"public-shares-events-detach": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveWorkspaceNonGuest"},
		note:   "publishing to a URL anyone may open is not a read-only role's decision",
	},
	"public-shares-events-reorder": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveWorkspaceNonGuest"},
		note:   "publishing to a URL anyone may open is not a read-only role's decision",
	},

	// Calendar-scoped surface. Access to a calendar is calendar_members,
	// which the workspace role says nothing about: a workspace admin is not
	// a member of a calendar nobody added them to, and a guest may well be a
	// member of one. Each handler resolves the calendar together with the
	// role that calendar demands.
	"calendars-patch": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarAdmin"},
		note:   "editing the calendar itself needs manager or above",
	},
	"calendars-delete": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarAtLeast"},
		note:   "deleting the calendar is the one action a manager does not get; the floor is owner",
	},
	"calendars-self-subscription-patch": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendar"},
		note:   "calendar membership; the row written is the caller's own display preference for it, which nobody else reads",
	},
	"events-create": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"events-patch": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"events-delete": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"events-smart-create": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendar"},
		note:   "calendar membership; the request parses text into a proposal and writes no row, so there is no shared state for a floor to protect — creating the proposed event is a separate call that does carry the editor floor",
	},
	"events-from-task": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"members-add": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarAdmin"},
		note:   "deciding who reaches the calendar needs manager or above",
	},
	"members-update-role": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarAdmin"},
		note:   "deciding who reaches the calendar needs manager or above",
	},
	"members-remove": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendar", "calendars.roleRank"},
		note:   "leaving is always allowed, so the role comparison is per-target rather than a floor on the route: removing anyone else needs manager or above, and removing an owner needs owner",
	},
	"attendees-add": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"attendees-remove": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"attendees-rsvp": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendar"},
		note:   "calendar membership; the handler then refuses anyone the event's attendee list does not name, and the only row it writes is that caller's own answer — a floor here would stop invitees replying to their own invitation",
	},
	"attendees-can-edit": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"event-invites-create": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"event-invites-revoke": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"comments-create": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"comments-edit": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"comments-delete": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"checklist-create": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"checklist-update": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"checklist-delete": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"memos-create": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"memos-update": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"memos-delete": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"attachments-presign": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"attachments-confirm": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
	"attachments-delete": {
		via:    auth.EnforcedByHandlerCall,
		checks: []string{"calendars.resolveCalendarWrite"},
		note:   "calendar editor or above, and never a provider-fed system calendar",
	},
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
		t.Errorf("mutating op %-6s %s (%s) has no workspace/project role floor — mount one via mountGroup, or add it to roleFloorExemptOps naming the check that authorises it instead",
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
//
// The service-token-only surface is outside the question entirely: no PAT or
// MCP token reaches it, bound or not, because its group refuses every bearer
// but the configured secret. That set is read off the router source rather
// than listed here, so an operation leaving the guarded group starts being
// classified again instead of keeping an exemption it no longer earns.
func TestBearerTokenWorkspaceBindingCoversEveryOp(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))
	serviceTokenOnly := serviceTokenOnlyOpIDs(parseHTTPSource(t))

	used := map[string]struct{}{}
	for _, op := range res.AuthenticatedOps {
		if serviceTokenOnly[op.OperationID] {
			continue
		}
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
