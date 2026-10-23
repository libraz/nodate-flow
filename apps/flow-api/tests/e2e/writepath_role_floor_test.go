package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// Writes that name a project in the body, or reach one through the row
// they change, have no path parameter for the project-role middleware to
// hang on. Each of them is held to the editor floor in the handler, and
// each pair below runs the same request as a member below the floor and as
// an editor, so a handler that refused everyone would not pass.

// projectRowCount counts rows of table bound to the tenant's default
// project, so a refused create can be shown to have written nothing.
func projectRowCount(t *testing.T, owner *helpers.TestTenant, table string) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM `+table+` x JOIN projects p ON p.id = x.project_id WHERE p.public_id = UUID_TO_BIN(?, 0)`,
		owner.ProjectPublicID,
	).Scan(&n))
	return n
}

// TestProjectScopedCreatesRequireProjectEditor drives page, timebox, lens,
// label and import creation into the owner's project as a workspace member
// with no project role and as a project viewer, then as an editor.
func TestProjectScopedCreatesRequireProjectEditor(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	outsider := seedWorkspaceMemberTenant(t, owner)
	viewer := seedProjectRoleMember(t, owner, "viewer")
	editor := seedProjectRoleMember(t, owner, "editor")
	wsBase := testServerURL + "/workspaces/" + owner.WorkspacePublicID

	creates := []struct {
		name  string
		table string
		path  string
		body  func() map[string]any
	}{
		{"page", "pages", "/pages", func() map[string]any {
			return map[string]any{"title": "floor page " + randomHex(4), "projectId": owner.ProjectPublicID}
		}},
		{"timebox", "timeboxes", "/timeboxes", func() map[string]any {
			return map[string]any{"name": "floor sprint " + randomHex(4), "startsOn": "2030-01-01", "endsOn": "2030-01-14", "projectId": owner.ProjectPublicID}
		}},
		{"lens", "lenses", "/lenses", func() map[string]any {
			return map[string]any{"name": "floor lens " + randomHex(4), "filter": json.RawMessage(`{}`), "sort": json.RawMessage(`[]`), "isDefault": false, "projectId": owner.ProjectPublicID}
		}},
		{"label", "labels", "/labels", func() map[string]any {
			return map[string]any{"name": "floor-" + randomHex(4), "projectId": owner.ProjectPublicID}
		}},
		{"import", "import_jobs", "/imports", func() map[string]any {
			return map[string]any{"source": "csv", "projectId": owner.ProjectPublicID, "configJson": map[string]any{"csv": "title\nfloor " + randomHex(4)}}
		}},
	}

	for _, c := range creates {
		t.Run(c.name, func(t *testing.T) {
			before := projectRowCount(t, owner, c.table)
			for _, denied := range []struct {
				label string
				as    *helpers.TestTenant
			}{{"no project role", outsider}, {"project viewer", viewer}} {
				status, body := doJSONStatus(t, http.MethodPost, wsBase+c.path, denied.as.AccessToken, c.body())
				requireDenied(t, status, body, http.StatusForbidden, "WS.PROJECT.ACCESS_DENIED", c.name+" as "+denied.label)
			}
			require.Equal(t, before, projectRowCount(t, owner, c.table), "a refused create must write no row")

			status, body := doJSONStatus(t, http.MethodPost, wsBase+c.path, editor.AccessToken, c.body())
			require.Equalf(t, http.StatusOK, status, "an editor must still create a %s; body=%s", c.name, string(body))
			require.Equal(t, before+1, projectRowCount(t, owner, c.table))
		})
	}
}

// TestReverseRequiresProjectEditor holds the reversal of agent activity on
// a task to the editor floor: a viewer can read the task and its timeline,
// which is not the right to undo what happened on it.
func TestReverseRequiresProjectEditor(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	viewer := seedProjectRoleMember(t, owner, "viewer")
	editor := seedProjectRoleMember(t, owner, "editor")

	taskID := createTaskWithVisibility(t, owner, "Reverse floor: public task", "public")
	agent := helpers.SeedAgent(t, testDB, owner.WorkspacePublicID, helpers.SeedAgentOptions{})
	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, owner.WorkspacePublicID, taskID)
	_, origPub := appendAgentEventForTask(t, testDB, wsID, taskInternalID, agent.AgentID, "ai.agent.run.completed")

	status, body := reverseStatusAs(t, owner.WorkspacePublicID, viewer, origPub)
	requireDenied(t, status, body, http.StatusForbidden, "WS.PROJECT.ACCESS_DENIED", "viewer reversing")

	var compensating int
	require.NoError(t, testDB.QueryRow(
		`SELECT COUNT(*) FROM events WHERE workspace_id = ? AND task_id = ? AND reverses_event_id IS NOT NULL`,
		wsID, taskInternalID,
	).Scan(&compensating))
	require.Zero(t, compensating, "a refused reversal must append no compensating event")

	status, body = reverseStatusAs(t, owner.WorkspacePublicID, editor, origPub)
	require.Equalf(t, http.StatusCreated, status, "an editor must still reverse; body=%s", string(body))
}

// TestImportWorkerReappliesTheInitiatorFloor covers the half of the import
// floor the API cannot hold: a job waits in the queue, and an initiator who
// loses the editor role in that time must not have it executed for them.
func TestImportWorkerReappliesTheInitiatorFloor(t *testing.T) {
	bootstrap(t)
	t.Parallel()
	ctx := context.Background()

	owner := newTenant(t)
	editor := seedProjectRoleMember(t, owner, "editor")
	title := "worker floor " + randomHex(6)

	var created importJobView
	doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+owner.WorkspacePublicID+"/imports", editor.AccessToken,
		map[string]any{"source": "csv", "projectId": owner.ProjectPublicID, "configJson": map[string]any{"csv": "title\n" + title}},
		&created)
	require.NotEmpty(t, created.ID)

	_, err := testDB.ExecContext(ctx, `
		UPDATE project_members pm
		JOIN projects p ON p.id = pm.project_id
		JOIN users u ON u.id = pm.user_id
		SET pm.role = 'viewer'
		WHERE p.public_id = UUID_TO_BIN(?, 0) AND u.public_id = UUID_TO_BIN(?, 0)`,
		owner.ProjectPublicID, editor.UserPublicID)
	require.NoError(t, err)

	var jobID uint32
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM import_jobs WHERE public_id = UUID_TO_BIN(?, 0)`, created.ID).Scan(&jobID))
	runImportWorkerFor(ctx, t, newImportWorker(t), jobID)

	var status string
	require.NoError(t, testDB.QueryRowContext(ctx, `SELECT status FROM import_jobs WHERE id = ?`, jobID).Scan(&status))
	require.Equal(t, string(generated.ImportJobsStatusFailed), status, "a job whose initiator lost the floor must fail")

	var tasks int
	require.NoError(t, testDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE title = ?`, title).Scan(&tasks))
	require.Zero(t, tasks, "a job failing the floor must create no task")
}

// TestConcurrentImportCreatesLeaveOneLiveJob races creates against one
// project: the one-live-job rule is decided under the project lock, so
// exactly one job is queued and the rest are told one is already running.
func TestConcurrentImportCreatesLeaveOneLiveJob(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	// A source with no connector stays pending until a worker fails it, so
	// the first job is still live for the others.
	create := raceCall{http.MethodPost, testServerURL + "/workspaces/" + owner.WorkspacePublicID + "/imports", owner.AccessToken,
		map[string]any{"source": "github", "projectId": owner.ProjectPublicID}}
	statuses := raceRequests(t, []raceCall{create, create, create, create})

	created := 0
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			created++
		case http.StatusConflict:
		default:
			require.Failf(t, "unexpected status", "got %d", s)
		}
	}
	require.Equal(t, 1, created, "exactly one concurrent create may queue a job")
	require.Equal(t, 1, projectRowCount(t, owner, "import_jobs"))
}

// TestRelationSuggestionDismissFloorAndRace holds dismiss to the member
// floor accept already has, and holds accept and dismiss racing on one
// suggestion to a single resolution.
func TestRelationSuggestionDismissFloorAndRace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := seedGuestMember(t, owner)
	src := createTaskIn(t, owner, "rs floor source")
	dst := createTaskIn(t, owner, "rs floor target")

	guarded := seedSuggestion(t, owner.WorkspacePublicID, src, dst, "relates")
	status, body := doJSONStatus(t, http.MethodPost, testServerURL+"/relation-suggestions/"+guarded+"/resolve",
		guest.AccessToken, map[string]any{"action": "dismiss"})
	requireDenied(t, status, body, http.StatusForbidden, "WS.MEMBER.ROLE_DENIED", "guest dismissing")

	// A second edge: uniq_relation_suggestions_edge allows one suggestion
	// per (source, target, kind).
	racedDst := createTaskIn(t, owner, "rs race target")
	raced := seedSuggestion(t, owner.WorkspacePublicID, src, racedDst, "relates")
	resolveURL := testServerURL + "/relation-suggestions/" + raced + "/resolve"
	results := raceRequests(t, []raceCall{
		{http.MethodPost, resolveURL, owner.AccessToken, map[string]any{"action": "accept"}},
		{http.MethodPost, resolveURL, owner.AccessToken, map[string]any{"action": "dismiss"}},
	})

	won := 0
	for _, s := range results {
		if s == http.StatusOK {
			won++
		} else {
			require.Equal(t, http.StatusConflict, s, "the losing resolution must be told it was already resolved")
		}
	}
	require.Equal(t, 1, won, "exactly one resolution may win")

	var resolutionEvents int
	require.NoError(t, testDB.QueryRow(`
		SELECT COUNT(*) FROM events
		WHERE type IN ('relation.accepted', 'relation.dismissed')
		  AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.suggestionId')) = ?`, raced).Scan(&resolutionEvents))
	require.Equal(t, 1, resolutionEvents, "one resolution, one event")
}
