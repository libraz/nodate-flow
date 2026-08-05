package e2e

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// seedSuggestion inserts a pending relation suggestion straight into the
// table. Suggestions are normally produced by the embedding pipeline,
// which is not wired in these tests; the row is all the accept endpoint
// reads, so writing it directly keeps the test about the endpoint.
func seedSuggestion(t *testing.T, workspacePublicID, sourceTaskID, targetTaskID, kind string) string {
	t.Helper()
	ctx := context.Background()

	wsID := internalWorkspaceID(t, testDB, workspacePublicID)
	var srcID, tgtID uint32
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`, sourceTaskID).Scan(&srcID))
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`, targetTaskID).Scan(&tgtID))

	pub, err := uuid.NewV7()
	require.NoError(t, err)
	bin, err := pub.MarshalBinary()
	require.NoError(t, err)
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO relation_suggestions
			(public_id, workspace_id, source_task_id, target_task_id, suggested_kind, confidence, status)
		VALUES (?, ?, ?, ?, ?, 0.9000, 'pending')`,
		bin, wsID, srcID, tgtID, kind)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testDB.Exec(`DELETE FROM relation_suggestions WHERE public_id = ?`, bin)
	})
	return pub.String()
}

// dependencyEdgeCount counts edges between two tasks in either
// direction, so an assertion cannot be satisfied by an edge that was
// written the other way round.
func dependencyEdgeCount(t *testing.T, fromTaskID, toTaskID string) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(`
		SELECT COUNT(*)
		FROM task_dependencies d
		JOIN tasks f ON f.id = d.from_task_id
		JOIN tasks tt ON tt.id = d.to_task_id
		WHERE f.public_id = UUID_TO_BIN(?, 0) AND tt.public_id = UUID_TO_BIN(?, 0)`,
		fromTaskID, toTaskID,
	).Scan(&n))
	return n
}

// createTaskIn creates a task in the tenant's default project.
func createTaskIn(t *testing.T, tt *helpers.TestTenant, title string) string {
	t.Helper()
	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken,
		map[string]any{"projectId": tt.ProjectPublicID, "title": title}, &task)
	require.NotEmpty(t, task.ID)
	return task.ID
}

// TestAcceptSuggestionRequiresProjectEditor pins the authorization floor
// on accepting a relation suggestion. Accepting one writes a real
// dependency edge, so it has to clear the same bar as asking for that
// edge directly: workspace membership on its own is not enough.
//
// A guest is the case that matters. They are a legitimate member of the
// workspace, so the endpoint's membership join was satisfied, and they
// could draw dependency edges between tasks in projects they had never
// been added to — edges that then gate other people's work through
// `dependency.all_done`.
func TestAcceptSuggestionRequiresProjectEditor(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	guest := seedGuestMember(t, owner)

	from := createTaskIn(t, owner, "Relation guard: source")
	to := createTaskIn(t, owner, "Relation guard: target")
	suggestion := seedSuggestion(t, owner.WorkspacePublicID, from, to, "blocks")

	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/relation-suggestions/"+suggestion+"/resolve",
		guest.AccessToken, map[string]any{"action": "accept"})
	require.Equal(t, http.StatusForbidden, status,
		"a guest must not turn a suggestion into a dependency edge, body=%s", string(raw))

	require.Zero(t, dependencyEdgeCount(t, from, to),
		"the refused accept must not have written an edge")

	// The owner, who is an editor in the project, still can.
	doJSON(t, http.MethodPost,
		testServerURL+"/relation-suggestions/"+suggestion+"/resolve",
		owner.AccessToken, map[string]any{"action": "accept"}, nil)
	require.Equal(t, 1, dependencyEdgeCount(t, from, to),
		"an authorized accept must still write the edge")
}

// TestAcceptSuggestionRejectsCycle is the second half, and the reason
// fixing authorization alone would not be enough: two mutual `blocks`
// suggestions accepted one after the other used to produce A→B and B→A.
//
// Nothing complains at the time. The constraint engine walks the graph
// with a cycle-tolerant BFS, so it neither crashes nor loops — it simply
// never reports `dependency.all_done` as satisfied again for either
// task, and no screen shows why.
func TestAcceptSuggestionRejectsCycle(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	owner := newTenant(t)
	a := createTaskIn(t, owner, "Cycle guard: A")
	b := createTaskIn(t, owner, "Cycle guard: B")

	forward := seedSuggestion(t, owner.WorkspacePublicID, a, b, "blocks")
	backward := seedSuggestion(t, owner.WorkspacePublicID, b, a, "blocks")

	// The first direction is a perfectly good edge.
	doJSON(t, http.MethodPost,
		testServerURL+"/relation-suggestions/"+forward+"/resolve",
		owner.AccessToken, map[string]any{"action": "accept"}, nil)
	require.Equal(t, 1, dependencyEdgeCount(t, a, b))

	// The mirror image closes the cycle and must be refused.
	status, raw := doJSONStatus(t, http.MethodPost,
		testServerURL+"/relation-suggestions/"+backward+"/resolve",
		owner.AccessToken, map[string]any{"action": "accept"})
	require.Equal(t, http.StatusUnprocessableEntity, status,
		"accepting the mirror suggestion must be rejected as a cycle, body=%s", string(raw))
	require.Contains(t, string(raw), "WS.TASK.DEPENDENCY_CYCLE",
		"the refusal must carry the canonical cycle code, body=%s", string(raw))

	require.Zero(t, dependencyEdgeCount(t, b, a),
		"the refused accept must not have written the back edge")

	// And the suggestion is still pending, not silently marked accepted.
	var status2 string
	require.NoError(t, testDB.QueryRow(
		`SELECT status FROM relation_suggestions WHERE public_id = UUID_TO_BIN(?, 0)`, backward,
	).Scan(&status2))
	require.Equal(t, "pending", status2,
		"a suggestion whose edge was refused must not be recorded as accepted")
}
