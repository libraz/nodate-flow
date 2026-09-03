// Layer 4 task visibility, checked against the routes the server
// actually serves rather than against a list of the ones we remembered.
//
// The recurring defect this guards is not a missing predicate in any one
// query — those get fixed. It is that `acl.TaskVisibilityFilter` and its
// .sql-file twin exist and are correct while the set of endpoints
// returning task titles keeps growing past the set that calls them. The
// same finding has now been raised three times, each time about
// different endpoints.
//
// So the route list here is not written down. It is read from the router
// source, and every GET is driven as a guest who may not see the task. Any
// response body carrying the task's title fails, whichever endpoint produced
// it — including endpoints added after this test was written.
//
// Every GET, not every GET the substitution table happens to cover. A route
// whose path parameter the table cannot fill, if skipped and merely
// reported, puts the blind spot in the log rather than in the result —
// and a whole surface goes unswept for as long as nobody reads it. A
// parameter with no substitution is therefore a failure, so a route
// added with a new parameter name has to be given a value before it
// merges.
package e2e

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// getRoute is one GET operation the server registers.
type getRoute struct {
	path    string
	opID    string
	srcFile string
}

// collectGETRoutes walks the flow-api HTTP source and returns every
// route registered with Method: http.MethodGet, read out of the
// huma.Operation literals themselves.
//
// The routes are taken from the source rather than from a list kept in
// this file, because a list is the thing that goes stale: the defect
// being guarded against is precisely an endpoint that exists and was
// never considered. Reading the same literals the server registers
// means a route added tomorrow is swept tomorrow.
//
// The alternative — asking the running server for /openapi.json — does
// not work here: the e2e harness serves flow-api behind a composite
// that answers that path from auth-api's spec.
func collectGETRoutes(t *testing.T) []getRoute {
	t.Helper()
	root := filepath.Join("..", "..", "internal", "http")
	var routes []getRoute
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Operation" {
				return true
			}
			var method, routePath, opID string
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "Method":
					if s, ok := kv.Value.(*ast.SelectorExpr); ok {
						method = s.Sel.Name
					}
				case "Path":
					if b, ok := kv.Value.(*ast.BasicLit); ok && b.Kind == token.STRING {
						routePath = strings.Trim(b.Value, `"`)
					}
				case "OperationID":
					if b, ok := kv.Value.(*ast.BasicLit); ok && b.Kind == token.STRING {
						opID = strings.Trim(b.Value, `"`)
					}
				}
			}
			if method == "MethodGet" && routePath != "" {
				routes = append(routes, getRoute{path: routePath, opID: opID, srcFile: path})
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	return routes
}

// absentResourceID is a well-formed id no fixture creates. It fills the
// parameters that name a resource the sweep does not stand up, so those
// routes still run their resolution and answer not-found instead of being
// left out of the sweep entirely.
const absentResourceID = "01930000-0000-7000-8000-000000000000"

// TestTaskListEndpointsHideInvisibleTitles sweeps every GET the server
// publishes, as a guest who is not a member of the project holding a
// private task, and fails on any response that carries its title.
//
// Path parameters are filled from a substitution table that has to be total
// over the routes: a parameter with no entry fails the test rather than
// dropping its routes. Where the parameter names a task, the private task's
// own id is substituted — the sharpest probe available, since an endpoint
// that resolves it without checking visibility answers with the title
// directly. The rest of the fixtures exist so the other surfaces answer with
// a real row instead of a lookup failure.
func TestTaskListEndpointsHideInvisibleTitles(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := seedGuestMember(t, owner)

	// The sentinel has to be a string no other fixture could produce,
	// because the assertion is a substring search over whole bodies.
	sentinel := "CONFIDENTIAL-SWEEP-" + randomHex(12)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
		"projectId":  owner.ProjectPublicID,
		"title":      sentinel,
		"visibility": "private",
	}, &task)
	require.NotEmpty(t, task.ID)

	// A second sentinel, archived. The live one never reaches
	// /workspaces/{wsId}/tasks/archived, so that route was driven
	// against an empty set and passed while returning every archived
	// private title to any workspace member. A task's state is part of
	// what the sweep has to cover, not just its visibility.
	archivedSentinel := "CONFIDENTIAL-ARCHIVED-" + randomHex(12)
	archivedTaskID := seedArchivedPrivateTask(t, owner, archivedSentinel)
	sentinels := map[string]string{
		"live":     sentinel,
		"archived": archivedSentinel,
	}

	// Put the task everywhere a list could pick it up from: a timebox,
	// a relation suggestion, an inbox signal, and a calendar event it is
	// linked to. Each of those was a separate hole; sweeping without
	// seeding them would pass on an empty set.
	timeboxID := seedTimeboxWithTask(t, owner, task.ID)
	seedRelationSuggestion(t, owner, task.ID, sentinel)
	seedInboxSignalForTask(t, owner, task.ID)
	calendarID, eventID := seedCalendarEventLinkedToTask(t, owner, task.ID)

	// Fixtures for the surfaces that were skipped for want of an id.
	// They carry no sentinel of their own; what they buy is that the
	// handler runs its projection rather than stopping at not-found.
	lensID := seedSweepLens(t, owner)
	pageID := seedSweepPage(t, owner)
	shareID, shareToken := seedSweepPublicShare(t, owner, eventID)

	// The guest must not be able to see it through the canonical route
	// either, or the sweep below proves nothing about the rest.
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID, guest.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND",
		"a non-actor guest reading a private task")

	status, body = doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+archivedTaskID, guest.AccessToken, nil)
	requireDenied(t, status, body, http.StatusNotFound, "WS.TASK.NOT_FOUND",
		"a non-actor guest reading an archived private task")

	routes := collectGETRoutes(t)
	require.Greater(t, len(routes), 50,
		"expected the router to register many GET routes; the AST walk found %d", len(routes))

	pathValues := map[string]string{
		// Rows this test creates, so the response has something to leak.
		"wsId":      owner.WorkspacePublicID,
		"prjId":     owner.ProjectPublicID,
		"id":        task.ID,
		"taskId":    task.ID,
		"timeboxId": timeboxID,
		"calId":     calendarID,
		"evtId":     eventID,
		"lensId":    lensID,
		"pageId":    pageID,
		"shareId":   shareID,
		"token":     shareToken,

		// Rows this test does not create. The request still reaches the
		// handler, which resolves the id and answers not-found.
		"actorId":       absentResourceID,
		"agentId":       absentResourceID,
		"aid":           absentResourceID,
		"attId":         absentResourceID,
		"attendeeId":    absentResourceID,
		"cId":           absentResourceID,
		"cid":           absentResourceID,
		"depId":         absentResourceID,
		"eventPublicId": absentResourceID,
		"importId":      absentResourceID,
		"inboxItemId":   absentResourceID,
		"inviteId":      absentResourceID,
		"itemId":        absentResourceID,
		"labelId":       absentResourceID,
		"linkId":        absentResourceID,
		"memoId":        absentResourceID,
		"notifId":       absentResourceID,
		"providerId":    absentResourceID,
		"reactionId":    absentResourceID,
		"snowflake":     "123456789012345678",
		"suggestionId":  absentResourceID,
		"tokenId":       absentResourceID,
		"userId":        absentResourceID,
		"versionId":     absentResourceID,
		"webhookId":     absentResourceID,
		"widgetId":      absentResourceID,
	}

	requireEveryPathParameterIsFillable(t, routes, pathValues)

	var swept []string
	for _, rt := range routes {
		url, filled := fillPath(rt.path, pathValues)
		if !filled {
			t.Errorf("GET %s (operation %s, %s) names a path parameter with no substitution, so the route goes unswept",
				rt.path, rt.opID, rt.srcFile)
			continue
		}
		// The range endpoints require start/end; without them the
		// request stops at validation and the route is not exercised.
		full := testServerURL + url + "?start=2020-01-01&end=2035-01-01&limit=200"

		status, body := doJSONStatus(t, http.MethodGet, full, guest.AccessToken, nil)
		swept = append(swept, fmt.Sprintf("%s (%d)", rt.path, status))
		for state, want := range sentinels {
			assert.NotContainsf(t, string(body), want,
				"GET %s (operation %s, %s) returned a %s private task's title to a guest who may not read it; status=%d",
				rt.path, rt.opID, rt.srcFile, state, status)
		}
	}

	sort.Strings(swept)
	require.NotEmpty(t, swept, "the sweep drove no routes; the router walk or the substitutions are wrong")
	require.Len(t, swept, len(routes), "every registered GET route has to be driven; the sweep is only as good as its coverage")
	t.Logf("swept %d GET routes", len(swept))
}

// requireEveryPathParameterIsFillable fails for each path parameter the
// substitution table has no value for, naming the routes that would be
// dropped. It runs before the sweep so the failure names the gap rather than
// the symptom.
func requireEveryPathParameterIsFillable(t *testing.T, routes []getRoute, values map[string]string) {
	t.Helper()
	missing := map[string][]string{}
	for _, rt := range routes {
		for _, name := range pathParameters(rt.path) {
			if _, ok := values[name]; !ok {
				missing[name] = append(missing[name], rt.path)
			}
		}
	}
	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		paths := missing[name]
		sort.Strings(paths)
		t.Errorf("path parameter {%s} has no substitution, so %d route(s) would go unswept: %s — add a value for it, seeding the resource where the endpoint needs a real one",
			name, len(paths), strings.Join(paths, ", "))
	}
}

// pathParameters returns the {name} placeholders a route template carries.
func pathParameters(path string) []string {
	var out []string
	rest := path
	for {
		open := strings.Index(rest, "{")
		if open < 0 {
			return out
		}
		closeIdx := strings.Index(rest[open:], "}")
		if closeIdx < 0 {
			return out
		}
		out = append(out, rest[open+1:open+closeIdx])
		rest = rest[open+closeIdx+1:]
	}
}

// fillPath substitutes {name} path parameters from the table. It reports
// false when the path names a parameter the table has no value for,
// which is how the caller decides to skip rather than send a request
// with a literal brace in the URL.
func fillPath(path string, values map[string]string) (string, bool) {
	out := path
	for {
		open := strings.Index(out, "{")
		if open < 0 {
			return out, true
		}
		closeIdx := strings.Index(out[open:], "}")
		if closeIdx < 0 {
			return out, false
		}
		name := out[open+1 : open+closeIdx]
		v, ok := values[name]
		if !ok {
			return out, false
		}
		out = out[:open] + v + out[open+closeIdx+1:]
	}
}

// seedArchivedPrivateTask creates a private task and archives it, so the
// archived listing has a row a guest must not be shown.
//
// The archive transition goes through the API rather than an UPDATE
// because archiving is what moves a task between the two views the list
// endpoints read from, and a direct write would leave the sweep proving
// something about a state the product cannot reach.
func seedArchivedPrivateTask(t *testing.T, owner *helpers.TestTenant, title string) string {
	t.Helper()
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
		"projectId":  owner.ProjectPublicID,
		"title":      title,
		"visibility": "private",
	}, &task)
	require.NotEmpty(t, task.ID)

	status, body := doJSONStatus(t, http.MethodPost,
		testServerURL+"/tasks/"+task.ID+"/archive", owner.AccessToken, nil)
	require.Equalf(t, http.StatusOK, status,
		"archiving the sentinel is what puts it in the archived listing; got %d: %s", status, body)
	return task.ID
}

// seedTimeboxWithTask creates a timebox and puts the task in it, so the
// timebox task list has something to leak.
func seedTimeboxWithTask(t *testing.T, owner *helpers.TestTenant, taskID string) string {
	t.Helper()
	var tb struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/timeboxes",
		owner.AccessToken, map[string]any{
			"name":     "sweep-timebox-" + randomHex(4),
			"startsOn": "2026-01-01",
			"endsOn":   "2026-12-31",
		}, &tb)
	if tb.ID == "" {
		return ""
	}
	doJSONStatus(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/timeboxes/"+tb.ID+"/tasks",
		owner.AccessToken, map[string]any{"taskId": taskID})
	return tb.ID
}

// seedRelationSuggestion writes a pending suggestion straight to the
// table. There is no API to create one — they come from the embedding
// pipeline — and the endpoint that reads them is one of the three the
// filter was missing from.
func seedRelationSuggestion(t *testing.T, owner *helpers.TestTenant, taskID, _ string) {
	t.Helper()
	var other struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", owner.AccessToken, map[string]any{
		"projectId":  owner.ProjectPublicID,
		"title":      "sweep public partner",
		"visibility": "public",
	}, &other)
	if other.ID == "" {
		return
	}
	_, err := testDB.Exec(
		`INSERT INTO relation_suggestions
		   (public_id, workspace_id, source_task_id, target_task_id, suggested_kind, confidence)
		 SELECT UUID_TO_BIN(UUID(), 0), s.workspace_id, s.id, tt.id, 'relates', 0.9
		   FROM tasks s, tasks tt
		  WHERE s.public_id = UUID_TO_BIN(?, 0)
		    AND tt.public_id = UUID_TO_BIN(?, 0)`,
		taskID, other.ID)
	require.NoError(t, err)
}

// seedCalendarEventLinkedToTask creates a calendar, an event on it, and the
// link between that event and the private task.
//
// The link is what makes the event side worth sweeping: the endpoints that
// answer from an event id project the task columns of everything linked to
// it, and an event id is reachable by any workspace member. The link row is
// written directly because the API that creates one requires the task to
// carry a due date, which is beside the point here.
func seedCalendarEventLinkedToTask(t *testing.T, owner *helpers.TestTenant, taskID string) (string, string) {
	t.Helper()
	var cal struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars",
		owner.AccessToken, map[string]any{
			"kind":  "personal",
			"name":  "sweep-calendar-" + randomHex(4),
			"color": "#4285F4",
		}, &cal)
	if cal.ID == "" {
		return "", ""
	}

	start := time.Date(2027, 3, 1, 9, 0, 0, 0, time.UTC)
	var evt struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/calendars/"+cal.ID+"/events",
		owner.AccessToken, map[string]any{
			"kind":     "event",
			"title":    "sweep umbrella event",
			"startAt":  start.Unix(),
			"endAt":    start.Add(time.Hour).Unix(),
			"timezone": "UTC",
		}, &evt)
	if evt.ID == "" {
		return cal.ID, ""
	}

	_, err := testDB.Exec(
		`INSERT INTO task_event_links
		   (public_id, workspace_id, task_id, event_id, relation)
		 SELECT UUID_TO_BIN(UUID(), 0), t.workspace_id, t.id, e.id, 'contributes_to'
		   FROM tasks t, calendar_events e
		  WHERE t.public_id = UUID_TO_BIN(?, 0)
		    AND e.public_id = UUID_TO_BIN(?, 0)`,
		taskID, evt.ID)
	require.NoError(t, err)
	return cal.ID, evt.ID
}

// seedSweepLens creates a lens so the lens routes resolve to a real row.
func seedSweepLens(t *testing.T, owner *helpers.TestTenant) string {
	t.Helper()
	var lens struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/lenses",
		owner.AccessToken, map[string]any{
			"name":      "sweep lens " + randomHex(4),
			"filter":    json.RawMessage(`{}`),
			"sort":      json.RawMessage(`[]`),
			"isDefault": false,
		}, &lens)
	return lens.ID
}

// seedSweepPage creates a page so the page routes resolve to a real row.
func seedSweepPage(t *testing.T, owner *helpers.TestTenant) string {
	t.Helper()
	var page struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/pages",
		owner.AccessToken, map[string]any{
			"title": "sweep page " + randomHex(4),
			"body":  "sweep body",
		}, &page)
	return page.ID
}

// seedSweepPublicShare creates a public share page carrying the seeded event,
// so both the workspace-side share routes and the token-rendered public page
// answer with content rather than a not-found.
func seedSweepPublicShare(t *testing.T, owner *helpers.TestTenant, eventID string) (string, string) {
	t.Helper()
	var share struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost,
		testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/public-shares",
		owner.AccessToken, map[string]any{"title": "sweep share " + randomHex(4)}, &share)
	if share.ID != "" && eventID != "" {
		doJSONStatus(t, http.MethodPost,
			testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/public-shares/"+share.ID+"/events",
			owner.AccessToken, map[string]any{"eventIds": []string{eventID}})
	}
	return share.ID, share.Token
}

// seedInboxSignalForTask attaches a signal to the task so the inbox
// feeds have a row whose task columns could leak.
func seedInboxSignalForTask(t *testing.T, _ *helpers.TestTenant, taskID string) {
	t.Helper()
	_, err := testDB.Exec(
		`INSERT INTO signals (public_id, workspace_id, task_id, source, kind, payload_json, received_at)
		 SELECT UUID_TO_BIN(UUID(), 0), t.workspace_id, t.id, 'manual', 'sweep.probe',
		        JSON_OBJECT('probe', 1), NOW(3)
		   FROM tasks t
		  WHERE t.public_id = UUID_TO_BIN(?, 0)`,
		taskID)
	require.NoError(t, err)
}
