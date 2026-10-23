package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint/engine"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// TestSignalReceivedConstraintIsMetByAnAttachedSignal pins that the
// signal.received fact is loaded from stored state: the constraint is
// unmet until a signal of the named kind is attached to the task, and
// met once one is.
func TestSignalReceivedConstraintIsMetByAnAttachedSignal(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "engine: signal fact",
	}, &task)
	require.NotEmpty(t, task.ID)

	var constraintRow struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/constraints",
		tt.AccessToken, map[string]any{
			"kind":       "signal",
			"expression": `{"op":"signal.received","arg":"pull_request.merged"}`,
		}, &constraintRow)
	require.NotEmpty(t, constraintRow.ID)

	satisfiedAt, _ := readConstraintMarkers(t, constraintRow.ID)
	require.False(t, satisfiedAt.Valid, "no signal is attached yet")

	wsID, taskInternalID := helpers.AgentTaskInternalIDs(t, testDB, tt.WorkspacePublicID, task.ID)
	_, err := testDB.Exec(
		`INSERT INTO signals (public_id, workspace_id, task_id, source, kind, external_id, payload_json,
			received_at, subject_type, subject_id)
		 VALUES (UUID_TO_BIN(UUID(), 0), ?, ?, 'github', 'pull_request.merged', ?, '{}',
			NOW(3), 'task', ?)`,
		wsID, taskInternalID, "settings-constraint-"+task.ID, taskInternalID)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	eng := &engine.Engine{Store: &engine.SqlcStore{WorkspaceID: wsID, Queries: generated.New(testDB)}}
	_, err = eng.EvaluateTask(ctx, taskInternalID)
	require.NoError(t, err)

	satisfiedAt, _ = readConstraintMarkers(t, constraintRow.ID)
	require.True(t, satisfiedAt.Valid, "an attached pull_request.merged signal satisfies the constraint")
}

// TestConstraintOpsWithNoFactSourceAreRefused pins that approval and
// CI-status constraints are refused at create: nothing records either
// fact, so a stored one could only ever stay unmet.
func TestConstraintOpsWithNoFactSourceAreRefused(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	var task struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "engine: unbacked ops",
	}, &task)
	require.NotEmpty(t, task.ID)

	for _, expr := range []string{
		`{"op":"approval.granted","arg":"lead"}`,
		`{"op":"ci.status_is","arg":"success"}`,
	} {
		status, _ := doJSONStatus(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/constraints",
			tt.AccessToken, map[string]any{"kind": "custom", "expression": expr})
		require.GreaterOrEqual(t, status, http.StatusBadRequest, "expression %s has no fact source", expr)
		require.Less(t, status, http.StatusInternalServerError, "expression %s must be refused, not fail", expr)
	}
}
