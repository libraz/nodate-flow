package e2e

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/constraint/engine"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/tests/helpers"
)

// createScopedTaskWithFacts builds a task in the tenant carrying every
// fact the constraint engine reads: a due date, an enabled constraint,
// an actor role, and an outgoing dependency to a second task. Returns
// the trigger task's public id.
func createScopedTaskWithFacts(t *testing.T, tt *helpers.TestTenant) (triggerPublicID, targetPublicID string) {
	t.Helper()

	var trigger struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "engine scope trigger",
		"dueOn":     "2026-12-31",
	}, &trigger)
	require.NotEmpty(t, trigger.ID)

	var target struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "engine scope target",
	}, &target)
	require.NotEmpty(t, target.ID)

	// Outgoing dependency (drives ListDependencyStatesForEngine).
	var dep struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+trigger.ID+"/dependencies",
		tt.AccessToken, map[string]any{
			"toTaskId": target.ID,
			"kind":     "blocks",
		}, &dep)
	require.NotEmpty(t, dep.ID)

	// Enabled constraint (drives ListTaskConstraintsForEngine).
	var constraint struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+trigger.ID+"/constraints",
		tt.AccessToken, map[string]any{
			"kind":       "deadline",
			"expression": `{"op":"time.due_before","arg":"2026-12-31"}`,
		}, &constraint)
	require.NotEmpty(t, constraint.ID)

	return trigger.ID, target.ID
}

// TestEngineReadsRejectForeignWorkspace proves C-3: the constraint
// engine's READ queries are scoped at the SQL boundary on workspace_id,
// not just by the upstream ACL middleware. We populate a task with every
// engine fact in tenant A, then drive the SqlcStore directly (bypassing
// every HTTP/ACL layer) with tenant B's workspace id. Every read must
// come back empty even though the row exists under the same internal
// task id, while the same call scoped to tenant A returns the facts.
func TestEngineReadsRejectForeignWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenantA := newTenant(t)
	tenantB := newTenant(t)

	triggerPub, _ := createScopedTaskWithFacts(t, tenantA)

	wsA := internalWorkspaceID(t, testDB, tenantA.WorkspacePublicID)
	wsB := internalWorkspaceID(t, testDB, tenantB.WorkspacePublicID)
	require.NotEqual(t, wsA, wsB)

	var taskID uint32
	require.NoError(t, testDB.QueryRowContext(context.Background(),
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`,
		triggerPub).Scan(&taskID))
	require.NotZero(t, taskID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	q := generated.New(testDB)

	// Foreign workspace (B): every read must be empty / not-found.
	storeForeign := &engine.SqlcStore{WorkspaceID: wsB, Queries: q}
	factsForeign, constraintsForeign, err := storeForeign.LoadTask(ctx, taskID)
	require.NoError(t, err, "LoadTask must not error on an empty result")
	require.Nil(t, factsForeign.DueOn, "due_on must not leak across workspaces")
	require.Empty(t, factsForeign.DependencyStates, "dependency states must not leak across workspaces")
	require.Empty(t, factsForeign.ActorRoles, "actor roles must not leak across workspaces")
	require.Empty(t, constraintsForeign, "constraints must not leak across workspaces")

	// Each underlying query individually scoped to B must also be empty.
	_, err = q.GetTaskDueOnForEngine(ctx, generated.GetTaskDueOnForEngineParams{ID: taskID, WorkspaceID: wsB})
	require.ErrorIs(t, err, sql.ErrNoRows, "GetTaskDueOnForEngine must miss for foreign workspace")
	depsB, err := q.ListDependencyStatesForEngine(ctx, generated.ListDependencyStatesForEngineParams{FromTaskID: taskID, WorkspaceID: wsB})
	require.NoError(t, err)
	require.Empty(t, depsB)
	rolesB, err := q.ListTaskActorRolesForEngine(ctx, generated.ListTaskActorRolesForEngineParams{TaskID: taskID, WorkspaceID: wsB})
	require.NoError(t, err)
	require.Empty(t, rolesB)
	consB, err := q.ListTaskConstraintsForEngine(ctx, generated.ListTaskConstraintsForEngineParams{TaskID: taskID, WorkspaceID: wsB})
	require.NoError(t, err)
	require.Empty(t, consB)

	// Sanity: the correct workspace (A) still returns the facts, so the
	// new scoping did not break the legitimate read path.
	storeOwn := &engine.SqlcStore{WorkspaceID: wsA, Queries: q}
	factsOwn, constraintsOwn, err := storeOwn.LoadTask(ctx, taskID)
	require.NoError(t, err)
	require.NotNil(t, factsOwn.DueOn, "owning workspace must see due_on")
	require.NotEmpty(t, factsOwn.DependencyStates, "owning workspace must see dependency states")
	require.NotEmpty(t, constraintsOwn, "owning workspace must see constraints")
}

// TestListPendingSuggestionsRejectForeignWorkspace proves the C-3 fix
// for ListPendingSuggestionsForTask: a relation suggestion bound to
// tenant A's workspace must not surface when the query is invoked with
// tenant B's workspace id, even though the internal task id is supplied
// verbatim. The earlier query correlated workspace_id to the joined task
// instead of an explicit parameter, so an ACL bypass would have returned
// the row.
func TestListPendingSuggestionsRejectForeignWorkspace(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tenantA := newTenant(t)
	tenantB := newTenant(t)

	triggerPub, targetPub := createScopedTaskWithFacts(t, tenantA)

	wsA := internalWorkspaceID(t, testDB, tenantA.WorkspacePublicID)
	wsB := internalWorkspaceID(t, testDB, tenantB.WorkspacePublicID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var srcID, tgtID uint32
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`, triggerPub).Scan(&srcID))
	require.NoError(t, testDB.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE public_id = UUID_TO_BIN(?, 0) LIMIT 1`, targetPub).Scan(&tgtID))

	// Insert a pending suggestion scoped to tenant A directly.
	sugPub, err := uuid.NewV7()
	require.NoError(t, err)
	sugBin, err := sugPub.MarshalBinary()
	require.NoError(t, err)
	_, err = testDB.ExecContext(ctx, `
		INSERT INTO relation_suggestions
			(public_id, workspace_id, source_task_id, target_task_id, suggested_kind, confidence, status)
		VALUES (?, ?, ?, ?, 'relates', 0.9000, 'pending')
	`, sugBin, wsA, srcID, tgtID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testDB.Exec(`DELETE FROM relation_suggestions WHERE public_id = ?`, sugBin)
	})

	q := generated.New(testDB)

	// Foreign workspace (B): no rows.
	foreign, err := q.ListPendingSuggestionsForTask(ctx, generated.ListPendingSuggestionsForTaskParams{
		WorkspaceID:  wsB,
		SourceTaskID: srcID,
		TargetTaskID: srcID,
		Limit:        50,
	})
	require.NoError(t, err)
	require.Empty(t, foreign, "relation suggestion must not surface for a foreign workspace")

	// Owning workspace (A): the suggestion is visible.
	own, err := q.ListPendingSuggestionsForTask(ctx, generated.ListPendingSuggestionsForTaskParams{
		WorkspaceID:  wsA,
		SourceTaskID: srcID,
		TargetTaskID: srcID,
		Limit:        50,
	})
	require.NoError(t, err)
	require.Len(t, own, 1, "owning workspace must see its own pending suggestion")
	require.Equal(t, sugPub.String(), own[0].PublicID.String())
}
