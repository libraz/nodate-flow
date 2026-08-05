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
// So the route list here is not written down. It is read from
// /openapi.json, which is generated from the router, and every GET whose
// path parameters this test can fill is driven as a guest who may not
// see the task. Any response body carrying the task's title fails,
// whichever endpoint produced it — including endpoints added after this
// test was written.
package e2e

import (
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

// TestTaskListEndpointsHideInvisibleTitles sweeps every GET the server
// publishes, as a guest who is not a member of the project holding a
// private task, and fails on any response that carries its title.
//
// Path parameters are filled from a substitution table. Where the
// parameter names a task, the private task's own id is substituted:
// that is the sharpest probe available, since an endpoint that resolves
// it without checking visibility answers with the title directly.
// Routes with a parameter the table cannot fill are skipped and
// reported, so the blind spots are visible rather than implied.
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

	// Put the task everywhere a list could pick it up from: a timebox,
	// a relation suggestion, and an inbox signal. Each of those was a
	// separate hole; sweeping without seeding them would pass on an
	// empty set.
	timeboxID := seedTimeboxWithTask(t, owner, task.ID)
	seedRelationSuggestion(t, owner, task.ID, sentinel)
	seedInboxSignalForTask(t, owner, task.ID)

	// The guest must not be able to see it through the canonical route
	// either, or the sweep below proves nothing about the rest.
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/tasks/"+task.ID, guest.AccessToken, nil)
	require.NotEqual(t, http.StatusOK, status,
		"a private task must not be readable by a non-actor guest; body=%s", string(body))

	routes := collectGETRoutes(t)
	require.Greater(t, len(routes), 50,
		"expected the router to register many GET routes; the AST walk found %d", len(routes))

	pathValues := map[string]string{
		"wsId":      owner.WorkspacePublicID,
		"id":        task.ID,
		"taskId":    task.ID,
		"projectId": owner.ProjectPublicID,
		"timeboxId": timeboxID,
	}

	var swept, skipped []string
	for _, rt := range routes {
		url, filled := fillPath(rt.path, pathValues)
		if !filled {
			skipped = append(skipped, rt.path)
			continue
		}
		// The range endpoints require start/end; without them the
		// request stops at validation and the route is not exercised.
		full := testServerURL + url + "?start=2020-01-01&end=2035-01-01&limit=200"

		status, body := doJSONStatus(t, http.MethodGet, full, guest.AccessToken, nil)
		swept = append(swept, fmt.Sprintf("%s (%d)", rt.path, status))
		assert.NotContains(t, string(body), sentinel,
			"GET %s (operation %s, %s) returned a private task's title to a guest who may not read it; status=%d",
			rt.path, rt.opID, rt.srcFile, status)
	}

	sort.Strings(swept)
	sort.Strings(skipped)
	require.NotEmpty(t, swept, "the sweep drove no routes; the spec or the substitutions are wrong")
	t.Logf("swept %d GET routes, skipped %d for unfillable path parameters", len(swept), len(skipped))
	t.Logf("skipped: %s", strings.Join(skipped, ", "))
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
