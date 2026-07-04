// Phase 6 / L2 — GET /workspaces/{wsId}/tasks/drafts?reason=retro coverage.
//
// The endpoint surfaces retrospective draft tasks the signal_judge
// Applier created (task + task_dependencies row of kind='retro_of' +
// TaskRetroDrafted event). These tests seed the three rows directly so
// the assertions stay focused on the handler / SQL shape rather than
// the full Applier pipeline (which has its own integration coverage
// under tests/signaljudge/applier_retro_test.go).
package e2e

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// retroDraftResponse mirrors the wire shape of ListRetroDraftsBody so
// the test can decode without pulling in the handler package's DTOs.
type retroDraftResponse struct {
	Total  int64 `json:"total"`
	Drafts []struct {
		TaskPublicID       string `json:"taskPublicId"`
		Title              string `json:"title"`
		Description        string `json:"description,omitempty"`
		CreatedAt          int64  `json:"createdAt"`
		CreatedByAgentID   string `json:"createdByAgentId,omitempty"`
		CreatedByAgentName string `json:"createdByAgentName,omitempty"`
		SourceTask         struct {
			PublicID string `json:"publicId"`
			Title    string `json:"title"`
		} `json:"sourceTask"`
	} `json:"drafts"`
}

// seedRetroDraft inserts a retro draft task plus its task_dependencies
// edge and the TaskRetroDrafted event in a single transaction. Returns
// (sourcePublicID, retroPublicID) — the test asserts on both.
func seedRetroDraft(t *testing.T, db *sql.DB, tt *helpers.TestTenant, agentInternalID uint32, sourceTitle, retroTitle string) (string, string) {
	t.Helper()

	wsID := lookupWorkspaceIDForDrafts(t, db, tt.WorkspacePublicID)
	projectID := lookupProjectIDForDrafts(t, db, wsID, tt.ProjectPublicID)

	tx, err := db.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	// Source task (the "completed" task that prompts a retrospective).
	srcPub := types.New()
	srcRes, err := tx.Exec(`
		INSERT INTO tasks (
			public_id, workspace_id, project_id, task_number,
			title, description, derived_state, visibility
		) VALUES (?, ?, ?, ?, ?, ?, 'open', 'public')`,
		srcPub, wsID, projectID, nextTaskNumber(t, tx, projectID),
		sourceTitle, sql.NullString{String: "Original task body", Valid: true},
	)
	require.NoError(t, err)
	srcInternalID, err := srcRes.LastInsertId()
	require.NoError(t, err)

	// Retro draft task (the "from" side of the retro_of edge).
	retroPub := types.New()
	retroRes, err := tx.Exec(`
		INSERT INTO tasks (
			public_id, workspace_id, project_id, task_number,
			title, description, derived_state, visibility
		) VALUES (?, ?, ?, ?, ?, ?, 'open', 'public')`,
		retroPub, wsID, projectID, nextTaskNumber(t, tx, projectID),
		retroTitle, sql.NullString{String: "Drafted retrospective body", Valid: true},
	)
	require.NoError(t, err)
	retroInternalID, err := retroRes.LastInsertId()
	require.NoError(t, err)

	// retro_of edge: from = the new retro task, to = the source task.
	depPub := types.New()
	_, err = tx.Exec(`
		INSERT INTO task_dependencies (
			public_id, workspace_id, from_task_id, to_task_id, kind, enabled
		) VALUES (?, ?, ?, ?, 'retro_of', TRUE)`,
		depPub, wsID, retroInternalID, srcInternalID,
	)
	require.NoError(t, err)

	// task.retro.drafted event with actor_agent_id so the handler's
	// FindRetroDraftAgent lookup resolves the agent attribution.
	evtPub := types.New()
	payload, err := json.Marshal(map[string]any{
		"newTaskPublicId":    retroPub.UUID().String(),
		"sourceTaskPublicId": srcPub.UUID().String(),
		"draft":              true,
	})
	require.NoError(t, err)
	_, err = tx.Exec(`
		INSERT INTO events (
			public_id, workspace_id, task_id, actor_agent_id,
			type, payload_json, occurred_at
		) VALUES (?, ?, ?, ?, 'task.retro.drafted', ?, NOW(3))`,
		evtPub, wsID, retroInternalID, agentInternalID, payload,
	)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	return srcPub.UUID().String(), retroPub.UUID().String()
}

// lookupWorkspaceIDForDrafts mirrors lookupWorkspaceIDForRetro in the
// signaljudge tests; duplicated here because the e2e package can't
// import that test-only helper.
func lookupWorkspaceIDForDrafts(t *testing.T, db *sql.DB, workspacePublicID string) uint32 {
	t.Helper()
	var id uint32
	require.NoError(t, db.QueryRow(
		`SELECT id FROM workspaces WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspacePublicID,
	).Scan(&id))
	require.NotZero(t, id)
	return id
}

// lookupProjectIDForDrafts returns the internal projects.id for the
// tenant's seeded default project.
func lookupProjectIDForDrafts(t *testing.T, db *sql.DB, workspaceID uint32, projectPublicID string) uint32 {
	t.Helper()
	var id uint32
	require.NoError(t, db.QueryRow(
		`SELECT id FROM projects
		 WHERE workspace_id = ? AND public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		workspaceID, projectPublicID,
	).Scan(&id))
	require.NotZero(t, id)
	return id
}

// nextTaskNumber returns the next per-project task_number inside the
// open transaction. Mirrors the AssignTaskNumber sqlc query without
// importing the generated package.
func nextTaskNumber(t *testing.T, tx *sql.Tx, projectID uint32) uint32 {
	t.Helper()
	var n sql.NullInt32
	require.NoError(t, tx.QueryRow(
		`SELECT MAX(task_number) FROM tasks WHERE project_id = ?`,
		projectID,
	).Scan(&n))
	if !n.Valid {
		return 1
	}
	require.GreaterOrEqual(t, n.Int32, int32(0))
	return uint32(n.Int32) + 1 //#nosec G115 -- bounded by non-negative INT task_number fixture above.
}

// TestListRetroDraftsHappyPath seeds a retro draft and asserts the
// endpoint returns its full shape — task fields, source backlink, and
// optional agent attribution resolved from the task.retro.drafted event.
func TestListRetroDraftsHappyPath(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	agent := helpers.SeedAgent(t, testDB, tt.WorkspacePublicID, helpers.SeedAgentOptions{Kind: "signal_judge"})

	srcPub, retroPub := seedRetroDraft(t, testDB, tt, agent.AgentID, "Production outage", "Retro: Production outage")

	var resp retroDraftResponse
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/tasks/drafts?reason=retro",
		tt.AccessToken, nil, &resp,
	)

	require.Equal(t, int64(1), resp.Total, "exactly one retro draft was seeded")
	require.Len(t, resp.Drafts, 1)

	d := resp.Drafts[0]
	require.Equal(t, retroPub, d.TaskPublicID)
	require.Equal(t, "Retro: Production outage", d.Title)
	require.Equal(t, "Drafted retrospective body", d.Description)
	require.Greater(t, d.CreatedAt, int64(0), "createdAt must be a real unix seconds value")

	require.Equal(t, srcPub, d.SourceTask.PublicID, "sourceTask.publicId must backlink to the source task")
	require.Equal(t, "Production outage", d.SourceTask.Title)

	require.Equal(t, agent.AgentPublicID, d.CreatedByAgentID,
		"createdByAgentId must surface from the task.retro.drafted event")
	require.Equal(t, agent.Name, d.CreatedByAgentName,
		"createdByAgentName must resolve from ai_agents.name")
}

// TestListRetroDraftsExcludesNonRetroTasks seeds a regular task with no
// retro_of edge and asserts it does not appear in the drafts feed.
func TestListRetroDraftsExcludesNonRetroTasks(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// Seed a vanilla task via the public REST API.
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "Regular task, no retro",
	}, &created)
	require.NotEmpty(t, created.ID)

	var resp retroDraftResponse
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/tasks/drafts?reason=retro",
		tt.AccessToken, nil, &resp,
	)

	require.Equal(t, int64(0), resp.Total, "vanilla task must not show up as a retro draft")
	require.Empty(t, resp.Drafts)
}

// TestListRetroDraftsRejectsUnknownReason pins the Huma enum boundary
// validation: any reason other than 'retro' must 422 before the handler
// runs.
func TestListRetroDraftsRejectsUnknownReason(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/tasks/drafts?reason=bogus",
		tt.AccessToken, nil,
	)
	require.GreaterOrEqualf(t, status, 400, "unknown reason must be a 4xx error; got %d body=%s", status, string(body))
	require.Lessf(t, status, 500, "unknown reason must be a 4xx, not a server fault; got %d body=%s", status, string(body))
}

// TestListRetroDraftsScopedToWorkspace pins workspace isolation: a
// retro draft seeded in workspace A must not surface when the endpoint
// is called from workspace B.
func TestListRetroDraftsScopedToWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenantA := newTenant(t)
	tenantB := newTenant(t)

	agentA := helpers.SeedAgent(t, testDB, tenantA.WorkspacePublicID, helpers.SeedAgentOptions{Kind: "signal_judge"})
	_, _ = seedRetroDraft(t, testDB, tenantA, agentA.AgentID, "WS-A source", "Retro: WS-A source")

	// Tenant B has no retro drafts. The endpoint must return an empty
	// list even though tenant A has one.
	var respB retroDraftResponse
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tenantB.WorkspacePublicID+"/tasks/drafts?reason=retro",
		tenantB.AccessToken, nil, &respB,
	)
	require.Equal(t, int64(0), respB.Total, "tenant B must not see tenant A's retro draft")
	require.Empty(t, respB.Drafts)

	// And tenant A's own call still works, confirming the seed isn't
	// silently broken.
	var respA retroDraftResponse
	doJSON(t, http.MethodGet,
		testServerURL+"/workspaces/"+tenantA.WorkspacePublicID+"/tasks/drafts?reason=retro",
		tenantA.AccessToken, nil, &respA,
	)
	require.Equal(t, int64(1), respA.Total)
	require.Len(t, respA.Drafts, 1)
}
